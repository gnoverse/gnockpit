.PHONY: install test test-page install-browsers typecheck

install:
	go install .

test:
	go test ./...

# The page suite runs in about 13 minutes on an M4 Pro and 23 on CI's macOS
# runner, past Go's default 10-minute timeout; 45 minutes covers that runner
# nearly twice over. -count=1
# runs it every time: Go's test cache knows nothing of the browsers, and a
# cached pass would launch none.
test-page:
	go vet -tags page ./web/
	go test -tags page -count=1 -timeout 45m ./web/

# --with-deps also installs the browsers' system packages, on the Linux
# distributions Playwright supports; set it empty to install the browsers
# alone: make install-browsers BROWSER_INSTALL_FLAGS=
BROWSER_INSTALL_FLAGS ?= --with-deps

install-browsers:
	go run github.com/mxschmitt/playwright-go/cmd/playwright install $(BROWSER_INSTALL_FLAGS) chromium webkit

# tsc checks the page's modules (web/js) as web/tsconfig.json sets it, with
# the TypeScript web/package-lock.json pins. A pass prints nothing: npm ci's
# summary goes to /dev/null, its errors to stderr, and tsc prints findings
# only.
typecheck:
	@npm --prefix web ci --no-audit --no-fund --loglevel=error >/dev/null
	@npm --prefix web run --silent typecheck
