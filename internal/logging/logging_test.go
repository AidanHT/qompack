package logging_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func todayLogPath(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "qompack-") && strings.HasSuffix(name, ".log") &&
			!strings.Contains(strings.TrimSuffix(strings.TrimPrefix(name, "qompack-"), ".log"), ".") {
			return filepath.Join(dir, name)
		}
	}
	t.Fatalf("no qompack-YYYYMMDD.log found in %s", dir)
	return ""
}

func TestLogger_LevelFiltering(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Warn)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()

	log.Debug("debug line")
	log.Info("info line")
	log.Warn("warn line")
	log.Error("error line")

	content := readFile(t, todayLogPath(t, dir))
	require.NotContains(t, content, "debug line")
	require.NotContains(t, content, "info line")
	require.Contains(t, content, "warn line")
	require.Contains(t, content, "error line")
}

func TestLoud_ThreeDestinations(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Info)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()

	fired := 0
	var gotMsg string
	var gotKV []any
	logging.AttachLoudObserver(func(msg string, kv ...any) {
		fired++
		gotMsg = msg
		gotKV = kv
	})
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })

	// A unique marker so this assertion cannot be confused with a Loud call from another test
	// sharing the same process-wide ring.
	marker := "contract broken TestLoud_ThreeDestinations"
	log.Loud(marker, "id", "x")

	dayContent := readFile(t, todayLogPath(t, dir))
	require.Contains(t, dayContent, marker)
	require.Contains(t, dayContent, "level=loud")
	require.Contains(t, dayContent, "id=x")

	loudContent := readFile(t, filepath.Join(dir, "LOUD.log"))
	require.Contains(t, loudContent, marker)
	require.Contains(t, loudContent, "id=x")

	found := false
	for _, line := range logging.LastLoud() {
		if strings.Contains(line, marker) {
			found = true
			break
		}
	}
	require.True(t, found, "LastLoud() must contain the just-recorded loud message")

	require.Equal(t, 1, fired, "the observer must fire exactly once")
	require.Equal(t, marker, gotMsg)
	require.Equal(t, []any{"id", "x"}, gotKV)
}

func TestLoud_NopStillRecordsRingAndObserver(t *testing.T) {
	fired := 0
	logging.AttachLoudObserver(func(msg string, kv ...any) { fired++ })
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })

	marker := "nop loud TestLoud_NopStillRecordsRingAndObserver"
	logging.Nop().Loud(marker)

	found := false
	for _, line := range logging.LastLoud() {
		if strings.Contains(line, marker) {
			found = true
		}
	}
	require.True(t, found)
	require.Equal(t, 1, fired)
}

func TestLoud_AttachNilClears(t *testing.T) {
	fired := 0
	logging.AttachLoudObserver(func(msg string, kv ...any) { fired++ })
	logging.AttachLoudObserver(nil)
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })

	logging.Nop().Loud("should not fire the cleared observer")
	require.Equal(t, 0, fired)
}

func TestLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.NewWithLimits(dir, logging.Info, 1, 2)
	require.NoError(t, err)

	// ~1 KB per line; ~3200 lines is comfortably over the 3 MB the spec asks for and well within
	// a fast test budget.
	payload := strings.Repeat("x", 1000)
	for i := 0; i < 3200; i++ {
		log.Info("rotation filler", "i", i, "payload", payload)
	}
	require.NoError(t, closer.Close())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var current, rotated int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "qompack-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(name, "qompack-"), ".log")
		if strings.Contains(mid, ".") {
			rotated++
		} else {
			current++
		}
	}
	require.Equal(t, 1, current, "exactly one current day file")
	require.LessOrEqual(t, rotated, 2, "at most MaxFiles=2 rotated files must survive pruning")
	require.Greater(t, rotated, 0, "3 MB at a 1 MB rotation threshold must have rotated at least once")
}

func TestLogger_With_AccumulatesFields(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Debug)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()

	child := log.With("component", "test")
	grandchild := child.With("req", "42")
	grandchild.Info("hello")
	child.Info("still just component") // parent handle must be unaffected by grandchild's field

	content := readFile(t, todayLogPath(t, dir))
	require.Contains(t, content, `msg="hello" component=test req=42`)
	require.Contains(t, content, `msg="still just component" component=test`)
	require.NotContains(t, content, `msg="still just component" component=test req=42`)
}

func TestLogger_ValueQuoting(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Debug)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()

	log.Info("quoting", "plain", "abc", "spaced", "has space", "eq", "a=b", "empty", "")

	content := readFile(t, todayLogPath(t, dir))
	require.Contains(t, content, "plain=abc")
	require.Contains(t, content, `spaced="has space"`)
	require.Contains(t, content, `eq="a=b"`)
	require.Contains(t, content, `empty=""`)
}

func TestLogger_New_RejectsEmptyDir(t *testing.T) {
	_, _, err := logging.New("", logging.Info)
	require.Error(t, err)
}

func TestLevel_StringAndParse(t *testing.T) {
	for _, tc := range []struct {
		lvl  logging.Level
		text string
	}{
		{logging.Debug, "debug"},
		{logging.Info, "info"},
		{logging.Warn, "warn"},
		{logging.Error, "error"},
		{logging.Loud, "loud"},
	} {
		require.Equal(t, tc.text, tc.lvl.String())
	}
	require.Equal(t, "unknown", logging.Level(255).String())

	for _, tc := range []struct {
		text string
		want logging.Level
	}{
		{"debug", logging.Debug},
		{"info", logging.Info},
		{"warn", logging.Warn},
		{"error", logging.Error},
	} {
		got, err := logging.ParseLevel(tc.text)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	_, err := logging.ParseLevel("loud")
	require.Error(t, err, "loud is not a configurable minimum level")
	_, err = logging.ParseLevel("bogus")
	require.Error(t, err)
}

func TestLogger_DayLogFileName(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Info)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()
	log.Info("hi")

	p := todayLogPath(t, dir)
	name := filepath.Base(p)
	require.True(t, strings.HasPrefix(name, "qompack-"))
	require.Len(t, name, len("qompack-YYYYMMDD.log"))
	require.Regexp(t, `^qompack-\d{8}\.log$`, name)
}

func TestLogger_ConcurrentWritesDoNotRace(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Debug)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()

	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			for i := 0; i < 50; i++ {
				log.Info("concurrent", "g", g, "i", i)
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}

func TestLogger_LoudBypassesMinLevel(t *testing.T) {
	dir := t.TempDir()
	// A minimum level above every non-Loud level still must not suppress Loud.
	log, closer, err := logging.New(dir, logging.Error)
	require.NoError(t, err)
	defer func() { require.NoError(t, closer.Close()) }()

	marker := fmt.Sprintf("loud-bypass-%d", os.Getpid())
	log.Loud(marker)
	content := readFile(t, todayLogPath(t, dir))
	require.Contains(t, content, marker)
}
