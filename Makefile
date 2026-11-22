.PHONY: install test test-page install-browsers

install:
	go install .

test:
	go test ./...

# The page suite runs in about 11 minutes on an M4 Pro, past Go's default
# 10-minute timeout; 30 minutes covers a CI runner twice as slow. -count=1
# runs it every time: Go's test cache knows nothing of the browsers, and a
# cached pass would launch none.
test-page:
	go vet -tags page ./web/
	go test -tags page -count=1 -timeout 30m ./web/

# --with-deps also installs the browsers' system packages, on the Linux
# distributions Playwright supports; set it empty to install the browsers
# alone: make install-browsers BROWSER_INSTALL_FLAGS=
BROWSER_INSTALL_FLAGS ?= --with-deps

install-browsers:
	go run github.com/mxschmitt/playwright-go/cmd/playwright install $(BROWSER_INSTALL_FLAGS) chromium webkit
