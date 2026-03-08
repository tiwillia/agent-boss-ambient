# Agent Boss - End-to-End Tests

Playwright-based end-to-end tests for the Agent Boss Coordinator web interface.

## Requirements

- **Node.js**: >=18.0.0 (tested with v20.20.0)
- **npm**: >=9.0.0 (tested with v10.8.2)
- **Boss binary**: Must be built at `/tmp/boss` (run `make build` from repository root)
- **System Dependencies**: Chromium requires system libraries (nss, nspr, libX11, etc.)

### Installing System Dependencies

On Ubuntu/Debian:
```bash
npx playwright install-deps chromium
```

Or install manually:
```bash
sudo apt-get install libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 \
  libcups2 libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 \
  libxrandr2 libgbm1 libasound2 libpangocairo-1.0-0 libpango-1.0-0
```

**Note**: Restricted container environments may lack these system libraries. E2E tests work best in full development environments or CI/CD systems with proper system package access.

## Installation

Install Playwright and dependencies:

```bash
npm install
```

Or use the Makefile target which handles installation automatically:

```bash
make test-e2e
```

## Running Tests

### Using Make (Recommended)

The Makefile target handles the complete test lifecycle:

```bash
make test-e2e
```

This will:
1. Verify Node.js and npm versions
2. Install Playwright dependencies if needed
3. Start the Boss server in background with ephemeral data directory
4. Wait for server to be ready (30s timeout)
5. Run Playwright tests
6. Cleanup: stop server and remove test data (even on failure)

### Manual Execution

If you need to run tests manually:

```bash
# Terminal 1: Start server
DATA_DIR=./e2e-data /tmp/boss serve

# Terminal 2: Run tests
cd e2e
npx playwright test

# Run in headed mode (visible browser)
npx playwright test --headed

# Run in debug mode
npx playwright test --debug
```

## Test Structure

```
e2e/
├── package.json                     # Playwright dependency
├── playwright.config.js             # Browser config, baseURL, timeouts
├── tests/
│   ├── space-crud.spec.js          # Space create/view/delete workflow
│   ├── agent-operations.spec.js    # Agent management operations
│   └── complete-workflows.spec.js  # End-to-end workflows
└── README.md                        # This file
```

## Test Coverage

### space-crud.spec.js

Tests the complete knowledge space lifecycle:
1. Navigate to mission control homepage
2. Create a new space with unique name
3. Verify space appears in list
4. Click space to view dashboard
5. Verify space header/title
6. Return to homepage
7. Delete the space
8. Verify space removed from list

### agent-operations.spec.js

Tests agent management operations:
1. **Create agent**: Launch agent modal, fill task prompt and repositories, verify agent created
2. **Edit agent**: Open edit modal, update task prompt/repos/heartbeat interval, verify changes saved
3. **Delete agent**: Click delete button, confirm deletion, verify agent removed
4. **Multi-agent**: Create multiple agents in one space, verify all agents coexist
5. **Launch session**: Verify UI flow for launching ACP sessions (requires ACP configuration)

Each test creates its own space and cleans up after completion.

### complete-workflows.spec.js

Tests end-to-end workflows:
1. **Multi-agent collaboration**: Create space with leader, developer, and reviewer agents, verify collaborative environment
2. **Agent + contracts**: Create agent, verify contracts section appears beneath agents (Issue #34)
3. **Heartbeat check-in**: Enable heartbeat interval via edit modal, verify feature works (Issue #29)
4. **Edit button fix**: Verify edit button opens modal with populated fields (Issue #30 regression test)

These tests validate complete user workflows and feature integrations.

## Configuration

See `playwright.config.js` for:
- Browser: Chromium only (lightweight for CI)
- Base URL: http://localhost:8899
- Timeouts: 10s action, 30s navigation
- Screenshots: On failure only
- Video: Retained on failure
- Retries: 1 on CI, 0 locally

## Troubleshooting

### Browser fails with missing shared libraries
```
error while loading shared libraries: libnspr4.so: cannot open shared object file
```
**Cause**: Missing system dependencies for Chromium
**Solution**: Install system dependencies (see Requirements section above)
```bash
npx playwright install-deps chromium
```
**Note**: This requires sudo/root access. In restricted containers without system package access, e2e tests cannot run. Use CI/CD or local development environment instead.

### Server won't start
- Ensure port 8899 is not already in use: `lsof -i :8899`
- Check Boss binary exists: `ls -l /tmp/boss`
- Run `make build` to rebuild

### Tests timeout
- Increase timeouts in playwright.config.js
- Check server logs for errors
- Verify baseURL is accessible: `curl http://localhost:8899`

### Cleanup issues
- The Makefile uses trap to ensure cleanup even on failure
- Manual cleanup if needed: `pkill -f '/tmp/boss'; rm -rf e2e-data`

## CI/CD Integration

For GitHub Actions or other CI:

```yaml
- name: Install Playwright browsers
  run: cd e2e && npx playwright install chromium --with-deps

- name: Run e2e tests
  run: make test-e2e
```

Note: Playwright will download Chromium (~200MB) on first install.
