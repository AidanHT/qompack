cp "$FIXTURE" imports.txt
product='^github.com/qompack/qompack/(internal|cmd)/'
# Test support: internal/testutil, internal/paths/pathstest, and every conformance package
# internal/<pkg>/<pkg>test.
support='^github.com/qompack/qompack/internal/(testutil|paths/pathstest|([a-z0-9]+)/\2test) '
rc=0
# No network stack anywhere; `net` itself is permitted only in internal/ipc (unix sockets).
if grep -E "$product" imports.txt | grep -Ev '^github.com/qompack/qompack/internal/ipc ' \
  | grep -E '\b(net/http|net/url|crypto/tls)\b'; then
  echo "::error::a product package imports a network stack (§8, D10)"; rc=1
fi
# os/exec allowlist: daemon (detached self-spawn), cli, testutil (§6.2 real-binary RunHook),
# and pathstest, which runs `go env` once to pin the toolchain's cache locations before it
# moves HOME (a9f276e7, b763d728). pathstest is test support exactly as testutil is.
if grep -E "$product" imports.txt \
  | grep -Ev '^github.com/qompack/qompack/internal/(daemon|cli|testutil|paths/pathstest) ' \
  | grep -E '\bos/exec\b'; then
  echo "::error::a product package outside the os/exec allow-list imports os/exec (§8)"; rc=1
fi
# Test support stays test-only: no product package imports testutil or pathstest.
if grep -E "$product" imports.txt | grep -Ev "$support" \
  | grep -E ' github.com/qompack/qompack/internal/(testutil|paths/pathstest)( |$)'; then
  echo "::error::a product package imports test support (internal/testutil or internal/paths/pathstest)"; rc=1
fi
exit "$rc"
