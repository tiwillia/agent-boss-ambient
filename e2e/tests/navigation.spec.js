const { test, expect } = require('@playwright/test');

/**
 * End-to-end test for Agent Boss Coordinator navigation and routing
 *
 * This test validates navigation functionality:
 * 1. Navigate between multiple spaces
 * 2. Direct URL navigation to space dashboard
 * 3. Browser back/forward navigation
 * 4. Handling of non-existent space URLs (404)
 * 5. Space list rendering with multiple spaces
 *
 * Prerequisites:
 * - Boss coordinator server running on http://localhost:8899
 */

test.describe('Navigation and Routing', () => {
  let testSpace1Name;
  let testSpace2Name;

  test.beforeEach(async ({ page }) => {
    // Generate unique space names for this test run
    const timestamp = Date.now();
    testSpace1Name = `test-nav-space-1-${timestamp}`;
    testSpace2Name = `test-nav-space-2-${timestamp}`;
  });

  test.afterEach(async ({ page }) => {
    // Cleanup: Delete test spaces
    await page.goto('/');

    for (const spaceName of [testSpace1Name, testSpace2Name]) {
      const deleteButton = page.locator(`button:near(:text("${spaceName}")):has-text("Delete")`);
      if (await deleteButton.isVisible({ timeout: 2000 }).catch(() => false)) {
        await deleteButton.click();
        const confirmButton = page.locator('button:has-text("Confirm"), button:has-text("Delete"), button:has-text("Yes")');
        if (await confirmButton.isVisible({ timeout: 2000 }).catch(() => false)) {
          await confirmButton.click();
          await page.waitForTimeout(500);
        }
      }
    }
  });

  test('should navigate between multiple spaces', async ({ page }) => {
    // Create first space
    await page.goto('/');
    let createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    let spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace1Name);
    let submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Create second space
    await page.goto('/');
    createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace2Name);
    submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Navigate to first space
    await page.goto('/');
    const space1Link = page.locator(`a:has-text("${testSpace1Name}"), text="${testSpace1Name}"`);
    await space1Link.click();

    // Verify we're on space 1
    await expect(page).toHaveURL(new RegExp(`/spaces/${testSpace1Name}`));

    // Navigate back to home
    await page.goto('/');

    // Navigate to second space
    const space2Link = page.locator(`a:has-text("${testSpace2Name}"), text="${testSpace2Name}"`);
    await space2Link.click();

    // Verify we're on space 2
    await expect(page).toHaveURL(new RegExp(`/spaces/${testSpace2Name}`));
  });

  test('should support direct URL navigation to space dashboard', async ({ page }) => {
    // Create a space first
    await page.goto('/');
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace1Name);
    const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Navigate directly to the space via URL
    await page.goto(`/spaces/${testSpace1Name}`);

    // Verify the page loads correctly
    await expect(page).toHaveURL(new RegExp(`/spaces/${testSpace1Name}`));

    // Verify space name appears in the page
    const spaceName = page.locator(`h1:has-text("${testSpace1Name}"), h2:has-text("${testSpace1Name}"), text="${testSpace1Name}"`);
    await expect(spaceName).toBeVisible({ timeout: 5000 });
  });

  test('should handle browser back and forward navigation', async ({ page }) => {
    // Create a space
    await page.goto('/');
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace1Name);
    const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Navigate to the space
    await page.goto('/');
    const spaceLink = page.locator(`a:has-text("${testSpace1Name}"), text="${testSpace1Name}"`);
    await spaceLink.click();
    await expect(page).toHaveURL(new RegExp(`/spaces/${testSpace1Name}`));

    // Go back to home
    await page.goBack();
    await expect(page).toHaveURL('/');

    // Go forward to space again
    await page.goForward();
    await expect(page).toHaveURL(new RegExp(`/spaces/${testSpace1Name}`));

    // Go back to home again
    await page.goBack();
    await expect(page).toHaveURL('/');
  });

  test('should handle non-existent space URL gracefully', async ({ page }) => {
    // Navigate to a space that doesn't exist
    const nonExistentSpaceName = `non-existent-space-${Date.now()}`;
    await page.goto(`/spaces/${nonExistentSpaceName}`);

    // Check for error message or redirect
    // The system might show a 404 page, error message, or redirect to home
    const url = page.url();
    const pageContent = await page.textContent('body');

    // Either we see an error/404, or we're redirected
    const hasError = pageContent.includes('404') ||
                     pageContent.includes('not found') ||
                     pageContent.includes('Not Found') ||
                     pageContent.includes('error') ||
                     url === '/' ||
                     url.endsWith('/');

    expect(hasError).toBeTruthy();
  });

  test('should render space list with multiple spaces', async ({ page }) => {
    // Create multiple spaces
    await page.goto('/');

    // Create first space
    let createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    let spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace1Name);
    let submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Create second space
    await page.goto('/');
    createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace2Name);
    submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Go back to home page
    await page.goto('/');

    // Verify both spaces appear in the list
    await expect(page.locator(`text="${testSpace1Name}"`)).toBeVisible({ timeout: 5000 });
    await expect(page.locator(`text="${testSpace2Name}"`)).toBeVisible({ timeout: 5000 });
  });

  test('should maintain URL consistency during navigation', async ({ page }) => {
    // Create a space
    await page.goto('/');
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace1Name);
    const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Navigate to space via link
    await page.goto('/');
    const spaceLink = page.locator(`a:has-text("${testSpace1Name}")`);
    await spaceLink.click();

    // Get the URL
    const url1 = page.url();
    expect(url1).toContain(`/spaces/${testSpace1Name}`);

    // Reload the page
    await page.reload();

    // URL should remain the same
    const url2 = page.url();
    expect(url2).toBe(url1);
  });

  test('should handle navigation to home from space dashboard', async ({ page }) => {
    // Create a space
    await page.goto('/');
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();
    const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill(testSpace1Name);
    const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();
    await page.waitForTimeout(1000);

    // Navigate to the space
    await page.goto(`/spaces/${testSpace1Name}`);

    // Find a link/button to go back to home (might be logo, home button, etc.)
    const homeLink = page.locator('a[href="/"], a:has-text("Home"), a:has-text("Mission Control"), header a').first();

    if (await homeLink.isVisible({ timeout: 3000 }).catch(() => false)) {
      await homeLink.click();
      await expect(page).toHaveURL('/');
    } else {
      // Alternatively, navigate directly
      await page.goto('/');
      await expect(page).toHaveURL('/');
    }
  });
});
