// @ts-check
const { defineConfig, devices } = require('@playwright/test');

/**
 * Playwright configuration for Agent Boss Coordinator e2e tests
 * @see https://playwright.dev/docs/test-configuration
 */
module.exports = defineConfig({
  testDir: './tests',

  /* Run tests in files in parallel */
  fullyParallel: false,

  /* Fail the build on CI if you accidentally left test.only in the source code */
  forbidOnly: !!process.env.CI,

  /* Retry on CI only */
  retries: process.env.CI ? 1 : 0,

  /* Opt out of parallel tests on CI */
  workers: process.env.CI ? 1 : undefined,

  /* Reporter to use */
  reporter: 'list',

  /* Shared settings for all the projects below */
  use: {
    /* Base URL to use in actions like `await page.goto('/')` */
    baseURL: 'http://localhost:8899',

    /* Collect trace when retrying the failed test */
    trace: 'on-first-retry',

    /* Screenshot on failure */
    screenshot: 'only-on-failure',

    /* Video on failure */
    video: 'retain-on-failure',
  },

  /* Configure projects for major browsers */
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        /* Default timeout for each test */
        actionTimeout: 10000,
        navigationTimeout: 30000,
      },
    },
  ],

  /* Run your local dev server before starting the tests */
  // NOTE: Server startup is handled by Makefile to ensure proper cleanup
  // webServer: {
  //   command: 'DATA_DIR=./e2e-data /tmp/boss serve',
  //   url: 'http://localhost:8899',
  //   timeout: 30 * 1000,
  //   reuseExistingServer: !process.env.CI,
  // },
});
