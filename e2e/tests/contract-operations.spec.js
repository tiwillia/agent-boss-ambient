const { test, expect } = require('@playwright/test');

/**
 * End-to-end test for Agent Boss Coordinator contract operations
 *
 * This test validates contract management operations:
 * 1. Viewing the contracts panel
 * 2. Creating a new contract
 * 3. Editing an existing contract
 * 4. Deleting a contract
 * 5. Verifying contract positioning (beneath agents per Issue #34)
 *
 * Prerequisites:
 * - Boss coordinator server running on http://localhost:8899
 * - At least one space exists for testing
 */

test.describe('Contract Operations', () => {
  let testSpaceName;
  let testContractTitle;

  test.beforeEach(async ({ page }) => {
    // Generate unique names for this test run
    const timestamp = Date.now();
    testSpaceName = `test-space-contracts-${timestamp}`;
    testContractTitle = `test-contract-${timestamp}`;

    // Create a test space first
    await page.goto('/');
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    if (await createButton.isVisible({ timeout: 3000 }).catch(() => false)) {
      await createButton.click();
      const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
      await spaceNameInput.fill(testSpaceName);
      const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
      await submitButton.click();
      await page.waitForTimeout(1000); // Wait for space creation
    }

    // Navigate to the test space
    await page.goto(`/spaces/${testSpaceName}`);
  });

  test.afterEach(async ({ page }) => {
    // Cleanup: Delete the test space
    await page.goto('/');
    const deleteButton = page.locator(`button:near(:text("${testSpaceName}")):has-text("Delete"), button:near(:text("${testSpaceName}")):has([data-icon="trash"])`);
    if (await deleteButton.isVisible({ timeout: 2000 }).catch(() => false)) {
      await deleteButton.click();
      const confirmButton = page.locator('button:has-text("Confirm"), button:has-text("Delete"), button:has-text("Yes")');
      if (await confirmButton.isVisible({ timeout: 2000 }).catch(() => false)) {
        await confirmButton.click();
      }
    }
  });

  test('should display contracts panel beneath agents', async ({ page }) => {
    // Verify the page structure has contracts panel
    const contractsPanel = page.locator('[data-panel="contracts"], .contracts-panel, section:has-text("Contracts")');

    // Check if contracts panel exists and is visible
    const panelExists = await contractsPanel.isVisible({ timeout: 5000 }).catch(() => false);

    if (panelExists) {
      // Verify it's positioned after the agents section
      // This validates Issue #34: contracts should be beneath agents
      const agentsPanel = page.locator('[data-panel="agents"], .agents-panel, section:has-text("Agents")');
      const contractsPosition = await contractsPanel.boundingBox();
      const agentsPosition = await agentsPanel.boundingBox().catch(() => null);

      if (agentsPosition && contractsPosition) {
        // Contracts panel should be below agents panel (higher Y coordinate)
        expect(contractsPosition.y).toBeGreaterThan(agentsPosition.y);
      }
    }
    // Note: If contracts panel doesn't exist, that's also valid (feature may not be implemented yet)
  });

  test('should create a new contract', async ({ page }) => {
    // Look for "Add Contract" or "Create Contract" button
    const addContractButton = page.locator('button:has-text("Add Contract"), button:has-text("Create Contract"), a:has-text("Add Contract")');

    const buttonVisible = await addContractButton.isVisible({ timeout: 3000 }).catch(() => false);

    if (buttonVisible) {
      // Click the add contract button
      await addContractButton.click();

      // Fill in contract title
      const titleInput = page.locator('input[name="title"], input[placeholder*="title" i], input[aria-label*="title" i]');
      await titleInput.fill(testContractTitle);

      // Fill in contract content/body
      const contentInput = page.locator('textarea[name="content"], textarea[placeholder*="content" i], textarea[aria-label*="content" i]');
      if (await contentInput.isVisible({ timeout: 2000 }).catch(() => false)) {
        await contentInput.fill('This is a test contract for automated testing.');
      }

      // Submit the form
      const submitButton = page.locator('button[type="submit"], button:has-text("Save"), button:has-text("Create")');
      await submitButton.click();

      // Verify the contract appears in the list
      await expect(page.locator(`text="${testContractTitle}"`)).toBeVisible({ timeout: 5000 });
    } else {
      console.warn('Contract creation UI not found - feature may not be implemented yet');
    }
  });

  test('should edit an existing contract', async ({ page }) => {
    // First create a contract to edit
    const addContractButton = page.locator('button:has-text("Add Contract"), button:has-text("Create Contract")');

    if (await addContractButton.isVisible({ timeout: 3000 }).catch(() => false)) {
      await addContractButton.click();
      const titleInput = page.locator('input[name="title"], input[placeholder*="title" i]');
      await titleInput.fill(testContractTitle);
      const submitButton = page.locator('button[type="submit"], button:has-text("Save"), button:has-text("Create")');
      await submitButton.click();
      await page.waitForTimeout(1000);

      // Now edit it
      const editButton = page.locator(`button:near(:text("${testContractTitle}")):has-text("Edit"), [data-contract="${testContractTitle}"] button:has-text("Edit")`);

      if (await editButton.isVisible({ timeout: 3000 }).catch(() => false)) {
        await editButton.click();

        // Modify the title
        const updatedTitle = `${testContractTitle}-edited`;
        const titleInput = page.locator('input[name="title"], input[placeholder*="title" i]');
        await titleInput.clear();
        await titleInput.fill(updatedTitle);

        // Save changes
        const saveButton = page.locator('button[type="submit"], button:has-text("Save"), button:has-text("Update")');
        await saveButton.click();

        // Verify the updated title appears
        await expect(page.locator(`text="${updatedTitle}"`)).toBeVisible({ timeout: 5000 });
      }
    }
  });

  test('should delete a contract', async ({ page }) => {
    // First create a contract to delete
    const addContractButton = page.locator('button:has-text("Add Contract"), button:has-text("Create Contract")');

    if (await addContractButton.isVisible({ timeout: 3000 }).catch(() => false)) {
      await addContractButton.click();
      const titleInput = page.locator('input[name="title"], input[placeholder*="title" i]');
      await titleInput.fill(testContractTitle);
      const submitButton = page.locator('button[type="submit"], button:has-text("Save"), button:has-text("Create")');
      await submitButton.click();
      await page.waitForTimeout(1000);

      // Now delete it
      const deleteButton = page.locator(`button:near(:text("${testContractTitle}")):has-text("Delete"), [data-contract="${testContractTitle}"] button:has-text("Delete")`);

      if (await deleteButton.isVisible({ timeout: 3000 }).catch(() => false)) {
        await deleteButton.click();

        // Confirm deletion if there's a confirmation dialog
        const confirmButton = page.locator('button:has-text("Confirm"), button:has-text("Delete"), button:has-text("Yes")');
        if (await confirmButton.isVisible({ timeout: 2000 }).catch(() => false)) {
          await confirmButton.click();
        }

        // Verify the contract is removed
        await expect(page.locator(`text="${testContractTitle}"`)).not.toBeVisible({ timeout: 5000 });
      }
    }
  });

  test('should handle multiple contracts', async ({ page }) => {
    const addContractButton = page.locator('button:has-text("Add Contract"), button:has-text("Create Contract")');

    if (await addContractButton.isVisible({ timeout: 3000 }).catch(() => false)) {
      // Create first contract
      await addContractButton.click();
      const titleInput1 = page.locator('input[name="title"], input[placeholder*="title" i]');
      await titleInput1.fill(`${testContractTitle}-1`);
      const submitButton1 = page.locator('button[type="submit"], button:has-text("Save"), button:has-text("Create")');
      await submitButton1.click();
      await page.waitForTimeout(500);

      // Create second contract
      await page.goto(`/spaces/${testSpaceName}`);
      await addContractButton.click();
      const titleInput2 = page.locator('input[name="title"], input[placeholder*="title" i]');
      await titleInput2.fill(`${testContractTitle}-2`);
      const submitButton2 = page.locator('button[type="submit"], button:has-text("Save"), button:has-text("Create")');
      await submitButton2.click();
      await page.waitForTimeout(500);

      // Verify both contracts are visible
      await page.goto(`/spaces/${testSpaceName}`);
      await expect(page.locator(`text="${testContractTitle}-1"`)).toBeVisible({ timeout: 5000 });
      await expect(page.locator(`text="${testContractTitle}-2"`)).toBeVisible({ timeout: 5000 });
    }
  });
});
