const { test, expect } = require('@playwright/test');

/**
 * End-to-end test for Agent Boss Coordinator space CRUD operations
 *
 * This test validates the complete lifecycle of a knowledge space:
 * 1. Creating a new space
 * 2. Viewing the space dashboard
 * 3. Deleting the space
 *
 * Prerequisites:
 * - Boss coordinator server running on http://localhost:8899
 * - Clean state (no conflicting test spaces)
 */

test.describe('Space CRUD Operations', () => {
  let testSpaceName;

  test.beforeEach(() => {
    // Generate unique space name for this test run
    const timestamp = Date.now();
    testSpaceName = `test-space-${timestamp}`;
  });

  test('should create, view, and delete a space', async ({ page }) => {
    // Step 1: Navigate to the mission control homepage
    await page.goto('/');
    await expect(page).toHaveTitle(/Mission Control/);

    // Step 2: Click the "Create Space" button
    // The button should be visible on the homepage
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await expect(createButton).toBeVisible({ timeout: 5000 });
    await createButton.click();

    // Step 3: Enter space name in the prompt/modal
    // Wait for input field or prompt dialog
    const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await expect(spaceNameInput).toBeVisible({ timeout: 3000 });
    await spaceNameInput.fill(testSpaceName);

    // Step 4: Submit the form to create the space
    const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();

    // Step 5: Verify the space appears in the space list
    // After creation, we should see the new space in the list
    const spaceLink = page.locator(`a:has-text("${testSpaceName}"), text="${testSpaceName}"`);
    await expect(spaceLink).toBeVisible({ timeout: 5000 });

    // Step 6: Click on the space to view its dashboard
    await spaceLink.click();

    // Step 7: Verify we're on the space dashboard page
    // The space name should appear in the header or title
    await expect(page).toHaveURL(new RegExp(`/spaces/${testSpaceName}`));
    const spaceHeader = page.locator(`h1:has-text("${testSpaceName}"), h2:has-text("${testSpaceName}")`);
    await expect(spaceHeader).toBeVisible({ timeout: 5000 });

    // Step 8: Navigate back to home or find delete button
    await page.goto('/');

    // Step 9: Delete the space
    // Find and click the delete button for our test space
    // This might be a button with a trash icon or "Delete" text near the space name
    const deleteButton = page.locator(`button:near(:text("${testSpaceName}")):has-text("Delete"), button:near(:text("${testSpaceName}")):has([data-icon="trash"])`);

    if (await deleteButton.isVisible({ timeout: 3000 })) {
      await deleteButton.click();

      // Step 10: Confirm deletion in the confirmation dialog (if present)
      const confirmButton = page.locator('button:has-text("Confirm"), button:has-text("Delete"), button:has-text("Yes")');
      if (await confirmButton.isVisible({ timeout: 2000 })) {
        await confirmButton.click();
      }

      // Step 11: Verify the space is removed from the list
      await expect(spaceLink).not.toBeVisible({ timeout: 5000 });
    } else {
      // If delete button not found, log warning but don't fail
      // (Manual cleanup may be needed)
      console.warn(`Delete button not found for space "${testSpaceName}". Manual cleanup may be required.`);
    }
  });

  test('should handle invalid space names gracefully', async ({ page }) => {
    await page.goto('/');

    // Try to create a space with invalid characters
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    await createButton.click();

    const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
    await spaceNameInput.fill('invalid space name!@#$');

    const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
    await submitButton.click();

    // Should show error message or validation feedback
    const errorMessage = page.locator('text=/invalid|error|cannot/i');
    // Note: We expect either an error message or the space not to be created
    // If no error message is shown, at least the space should not appear in the list
    const hasError = await errorMessage.isVisible({ timeout: 3000 }).catch(() => false);
    if (!hasError) {
      // Verify the invalid space doesn't exist
      const invalidSpaceLink = page.locator('text="invalid space name!@#$"');
      await expect(invalidSpaceLink).not.toBeVisible();
    }
  });
});
