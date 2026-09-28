package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// `qompack mcp`: the stdio MCP server the host launches from plugin/.mcp.json.
//
// This process is a TRANSCODER, not a second implementation. It speaks JSON-RPC 2.0 over stdin and
// stdout, advertises the eight tools of §8.7 from the same mcp.ToolDefs the daemon registers — so
// `tools/list` is byte-identical on both sides — and forwards every `tools/call` over the local
// transport to the daemon, where the warm handles and the single-writer discipline live.
//
// Two rules govern it, and both are absolute. NOTHING but JSON-RPC lines may reach stdout: the
// host parses that stream, and one stray diagnostic ends the session. And nothing is ever spooled:
// a retrieval is request/response or it is nothing (see nopSpool).

// mcpRetryDelay is how long forward waits between attempts while a lazily-spawned daemon comes up.
// It is time.After rather than time.Sleep on its merits, not to satisfy a linter: a long-lived
// server must stay cancellable, and a sleeping goroutine is not.
const mcpRetryDelay = 150 * time.Millisecond

// mcpRetryAttempts bounds that wait. Ten attempts at 150 ms is 1.5 seconds — comfortably longer
// than a cold daemon start, and short enough that a model asking a question gets an answer within
// its own patience rather than a hang.
const mcpRetryAttempts = 10

// mcpCallDeadline is how long one forwarded tools/call may take, matching the daemon's own bound
// so neither side abandons a call the other is still serving.
const mcpCallDeadline = 5 * time.Second

// daemonUnavailableMsg is what the model is told when the daemon cannot be reached. It says the
// store is intact on purpose: the failure is transport-level and temporary, and a model that reads
// "retrieval is broken" stops trying for the rest of the session.
const daemonUnavailableMsg = "qompack daemon unavailable; retrieval is temporarily offline — " +
	"the store is intact and the same call will succeed once the daemon is running"

// runMCP implements `qompack mcp`.
//
// It is not a hook and it is not short-lived: it runs until the host closes stdin. It exits 0 on a
// clean shutdown — EOF, SIGINT or SIGTERM — and non-zero only when stdio itself could not be
// served, because every other failure has a JSON-RPC answer.
func runMCP(ctx context.Context, env Env, _ []string, out, errw io.Writer) error {
	root := resolveProjectRoot(env, nil)
	if root == "" {
		fmt.Fprintln(errw, "qompack mcp: could not resolve a project root")
		return nil
	}
	if refused := refuseHomeRoot(env, root); refused != nil {
		return serveRefusedMCP(ctx, env, refused, out, errw)
	}

	l := paths.Of(root)
	if err := paths.EnsureLayout(l); err != nil {
		fmt.Fprintf(errw, "qompack mcp: could not create %s: %v\n", l.Dot, err)
		return nil
	}

	// The logger goes to .qompack/logs/ and NOWHERE else. A logger that fell back to stderr would
	// still be safe, but one that fell back to stdout would corrupt the JSON-RPC stream, so the
	// fallback is a no-op logger rather than a different writer.
	log, closer, err := logging.New(l.Logs, logging.Info)
	if err != nil {
		log = logging.Nop()
	} else {
		defer func() { _ = closer.Close() }()
	}

	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	reg := obs.New(clk)
	AttachLoudCounter(reg)

	cfg, _, cfgErr := LoadConfigAndReport(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	}, log, reg)
	if cfgErr != nil {
		cfg = config.Defaults()
		log.Loud("mcp: could not load configuration, using defaults", "err", cfgErr.Error())
	}

	client := newMCPClient(root, cfg, env, log, reg, clk)
	defer func() { _ = client.Close() }()

	srv, err := buildMCPProxy(ctx, root, cfg, client, log)
	if err != nil {
		fmt.Fprintf(errw, "qompack mcp: could not build the tool set: %v\n", err)
		return err
	}

	serveCtx, cancel := signalContext(ctx)
	defer cancel()

	if err := srv.Serve(serveCtx, env.Stdin, out); err != nil && !errors.Is(err, io.EOF) {
		log.Warn("mcp: the stdio server stopped", "err", err.Error())
		return err
	}
	return nil
}

// serveRefusedMCP serves a session refused under D18: its project root is the home directory.
//
// The server still speaks JSON-RPC and still lists the same eight tools, so the host's view of the
// plugin does not change between directories and no configuration has to be edited; every
// tools/call answers mcp.HomeRootRefusedText as a tool error the model reads. Nothing else happens:
// no layout, no log file, no handshake record, no configuration report and no client, so no daemon
// is ever started for the home directory. stderr, which the host keeps apart from the protocol
// stream, says why once.
func serveRefusedMCP(ctx context.Context, env Env, refused error, out, errw io.Writer) error {
	fmt.Fprintf(errw, "qompack mcp: %v\n", refused)
	srv := mcp.NewServerWithOptions(mcp.ServerOptions{
		Name:    mcp.ServerName,
		Version: core.Version,
		Log:     logging.Nop(),
		MaxLine: config.Defaults().Runtime.HotPath.MaxPayloadBytes,
	})
	if err := mcp.RegisterProxy(srv, refusedMCPCall); err != nil {
		fmt.Fprintf(errw, "qompack mcp: could not build the tool set: %v\n", err)
		return err
	}
	serveCtx, cancel := signalContext(ctx)
	defer cancel()
	if err := srv.Serve(serveCtx, env.Stdin, out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// refusedMCPCall is the handler every tool is bound to in a refused session.
func refusedMCPCall(context.Context, mcp.Request) (mcp.Response, error) {
	return mcp.Response{
		IsError: true,
		Content: []mcp.Content{{Type: "text", Text: mcp.HomeRootRefusedText}},
	}, nil
}

// newMCPClient builds the transport `qompack mcp` forwards over, using SP-05's own lazy-spawn seam
// rather than a hand-rolled one: the first Send starts a detached daemon when none is listening,
// and forward's retry loop is what waits for it.
func newMCPClient(root string, cfg config.Config, env Env,
	log logging.Logger, reg obs.Registry, clk core.Clock,
) ipc.Client {
	addr, _ := ipc.Resolve(root)
	return ipc.NewClientWithOptions(addr, nopSpool{}, log, reg, ipc.ClientOptions{
		ProjectRoot: root,
		State:       daemonClientState(root, cfg),
		Self:        env.Self,
		Spawn:       spawnDaemon,
		Clock:       clk,
	})
}

// buildMCPProxy assembles the stdio server: the eight tool definitions with one forwarding
// handler, plus the initialize callback that records the handshake on both sides.
func buildMCPProxy(ctx context.Context, root string, cfg config.Config,
	client ipc.Client, log logging.Logger,
) (mcp.Server, error) {
	// Declared before the server so it can be handed in through ServerOptions — which is the whole
	// reason NewServerWithOptions exists rather than a method on the §5.16 Server interface.
	onInit := func(o mcp.Observable) {
		if err := mcp.WriteInitializedObservable(root, o); err != nil {
			log.Warn("mcp: could not write the handshake record", "err", err.Error())
		}
		raw, merr := json.Marshal(daemon.MCPOpRequest{Kind: daemon.MCPKindInitialized, Observ: &o})
		if merr != nil {
			return
		}
		_, _ = client.Send(ctx, ipc.Request{Op: ipc.OpMCP, Raw: raw}, mcpCallDeadline)
	}

	srv := mcp.NewServerWithOptions(mcp.ServerOptions{
		Name:         mcp.ServerName,
		Version:      core.Version,
		Log:          log,
		MaxLine:      cfg.Runtime.HotPath.MaxPayloadBytes,
		OnInitialize: onInit,
	})
	if err := mcp.RegisterProxy(srv, forwardMCPCall(client, log)); err != nil {
		return nil, err
	}
	return srv, nil
}

// forwardMCPCall returns the handler every proxied tool is bound to.
//
// Session is left EMPTY on purpose. The stdio process has no session identity to send — the host
// hands an MCP server no session_id — and the daemon is the only party that can resolve one, from
// its own registry. Sending a guess would be worse than sending nothing.
func forwardMCPCall(client ipc.Client, log logging.Logger) mcp.Handler {
	return func(ctx context.Context, r mcp.Request) (mcp.Response, error) {
		raw, err := json.Marshal(daemon.MCPOpRequest{Kind: daemon.MCPKindCall, Name: r.Name, Args: r.Args})
		if err != nil {
			return mcp.Response{}, err
		}
		req := ipc.Request{Op: ipc.OpMCP, Reply: true, Raw: raw}

		for attempt := 0; attempt < mcpRetryAttempts; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return unavailableResponse(), nil
				case <-time.After(mcpRetryDelay):
				}
			}
			resp, serr := client.Send(ctx, req, mcpCallDeadline)
			if serr != nil || !resp.OK {
				continue
			}
			return decodeMCPOpResponse(resp.Data, log)
		}
		return unavailableResponse(), nil
	}
}

// decodeMCPOpResponse turns the daemon's payload back into a handler Response.
func decodeMCPOpResponse(data json.RawMessage, log logging.Logger) (mcp.Response, error) {
	var payload daemon.MCPOpResponse
	if len(data) == 0 {
		return mcp.Response{
			IsError: true,
			Content: []mcp.Content{{Type: "text", Text: "the qompack daemon returned an empty result"}},
		}, nil
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		log.Warn("mcp: could not decode the daemon's tool result", "err", err.Error())
		return mcp.Response{
			IsError: true,
			Content: []mcp.Content{{Type: "text", Text: "the qompack daemon returned an unreadable result"}},
		}, nil
	}
	return mcp.Response{
		Content:   payload.Content,
		IsError:   payload.IsError,
		Ephemeral: payload.Ephemeral,
		Meta:      payload.Meta,
	}, nil
}

// unavailableResponse is the tool error a model reads when the daemon could not be reached.
func unavailableResponse() mcp.Response {
	return mcp.Response{
		IsError: true,
		Content: []mcp.Content{{Type: "text", Text: daemonUnavailableMsg}},
	}
}

// signalContext derives a context cancelled by SIGINT or SIGTERM, so the host's own shutdown ends
// the loop cleanly rather than killing a process mid-write.
func signalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	serveCtx, cancel := context.WithCancel(ctx)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(sigCh)
		select {
		case <-sigCh:
			cancel()
		case <-serveCtx.Done():
		}
	}()
	return serveCtx, cancel
}
