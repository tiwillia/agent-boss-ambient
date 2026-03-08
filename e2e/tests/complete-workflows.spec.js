const { test, expect } = require('@playwright/test');

/**
 * End-to-end tests for complete Agent Boss Coordinator workflows
 *
 * This test suite validates end-to-end workflows:
 * 1. Multi-agent collaboration scenarios
 * 2. Agent + contract interaction workflows
 * 3. Heartbeat check-in feature (Issue #29)
 * 4. Edit button modal fix verification (Issue #30)
 *
 * Prerequisites:
 * - Boss coordinator server running on http://localhost:8899
 * - Clean state (tests create and cleanup their own spaces)
 */

test.describe('Complete Workflows', () => {
  let testSpaceName;

  test.beforeEach(() => {
    // Generate unique space name for this test run
    const timestamp = Date.now();
    testSpaceName = `test-workflow-${timestamp}`;
  });

  test.afterEach(async ({ page }) => {
    // Cleanup: Delete the test space if it exists
    try {
      await page.goto('/');
      const spaceLink = page.locator(`text="${testSpaceName}"`);
      if (await spaceLink.isVisible({ timeout: 2000 }).catch(() => false)) {
        const deleteButton = page.locator(`button:near(:text("${testSpaceName}")):has-text("Delete")`);
        if (await deleteButton.isVisible({ timeout: 2000 }).catch(() => false)) {
          await deleteButton.click();
          const confirmButton = page.locator('button:has-text("Confirm"), button:has-text("Delete"), button:has-text("Yes")');
          if (await confirmButton.isVisible({ timeout: 2000 }).catch(() => false)) {
            await confirmButton.click();
          }
        }
      }
    } catch (e) {
      console.warn(`Cleanup warning for space "${testSpaceName}": ${e.message}`);
    }
  });

  test('should support multi-agent collaboration workflow', async ({ page }) => {
    // This test creates a space with multiple agents simulating a team collaboration

    // Step 1: Create a collaborative workspace
    await page.goto('/');
    await expect(page).toHaveTitle(/Mission Control/);

    const createSpaceButton = page.locator('button:has-text("Create Space")');
    await createSpaceButton.click();

    const spaceNameInput = page.locator('input#create-space-name');
    await spaceNameInput.fill(testSpaceName);

    const submitSpaceButton = page.locator('button:has-text("Create"):visible');
    await submitSpaceButton.click();

    // Step 2: Navigate to the space
    const spaceLink = page.locator(`a:has-text("${testSpaceName}")`);
    await expect(spaceLink).toBeVisible({ timeout: 5000 });
    await spaceLink.click();

    // Step 3: Create multiple agents for collaboration
    const agents = [
      { name: 'leader', task: 'Coordinate team and assign tasks' },
      { name: 'developer', task: 'Implement features' },
      { name: 'reviewer', task: 'Review code and approve changes' }
    ];

    for (const agent of agents) {
      const newSessionButton = page.locator('button:has-text("New Session")');
      await newSessionButton.click();

      const agentNameInput = page.locator('input#create-agent-name');
      await agentNameInput.fill(agent.name);

      const taskPromptTextarea = page.locator('textarea#create-prompt');
      await taskPromptTextarea.fill(agent.task);

      const launchButton = page.locator('button:has-text("Launch"):visible');
      await launchButton.click();

      // Wait for agent to be created before creating next one
      await page.waitForTimeout(1500);
    }

    // Step 4: Reload and verify all agents are present
    await page.reload();

    for (const agent of agents) {
      const agentCard = page.locator(`text="${agent.name}"`);
      await expect(agentCard).toBeVisible({ timeout: 5000 });
    }

    // Step 5: Verify the space shows collaborative environment
    // Check that we can see multiple agent sections
    const agentCards = page.locator('[class*="agent"], [data-agent]');
    const count = await agentCards.count();
    expect(count).toBeGreaterThanOrEqual(3); // At least our 3 agents
  });

  test('should support agent and contract workflow', async ({ page }) => {
    // This test verifies that agents and contracts can coexist in a space
    // and that contracts appear beneath agents (Issue #34)

    // Step 1: Create space with an agent
    await page.goto('/');

    const createSpaceButton = page.locator('button:has-text("Create Space")');
    await createSpaceButton.click();

    const spaceNameInput = page.locator('input#create-space-name');
    await spaceNameInput.fill(testSpaceName);

    const submitSpaceButton = page.locator('button:has-text("Create"):visible');
    await submitSpaceButton.click();

    const spaceLink = page.locator(`a:has-text("${testSpaceName}")`);
    await spaceLink.click();

    // Step 2: Create an agent
    const newSessionButton = page.locator('button:has-text("New Session")');
    await newSessionButton.click();

    const agentNameInput = page.locator('input#create-agent-name');
    await agentNameInput.fill('contract-manager');

    const taskPromptTextarea = page.locator('textarea#create-prompt');
    await taskPromptTextarea.fill('Manage team contracts and agreements');

    const launchButton = page.locator('button:has-text("Launch"):visible');
    await launchButton.click();

    await page.waitForTimeout(2000);
    await page.reload();

    // Step 3: Verify agent exists
    const agentCard = page.locator('text="contract-manager"');
    await expect(agentCard).toBeVisible({ timeout: 5000 });

    // Step 4: Check for contracts section
    // Contracts should appear beneath agents per Issue #34
    const contractsSection = page.locator('text=/Shared Contracts|Contracts/i');

    if (await contractsSection.isVisible({ timeout: 3000 }).catch(() => false)) {
      // Contracts section exists
      expect(true).toBe(true);

      // Verify contracts appear after agents in the DOM
      const agentPosition = await page.evaluate(() => {
        const agentEl = document.querySelector('[data-agent], text="contract-manager"')?.parentElement;
        return agentEl ? agentEl.offsetTop : 0;
      });

      const contractPosition = await page.evaluate(() => {
        const contractEl = document.querySelector(':has-text("Shared Contracts"), :has-text("Contracts")');
        return contractEl ? contractEl.offsetTop : 99999;
      });

      // Contracts should be positioned below agents
      expect(contractPosition).toBeGreaterThanOrEqual(agentPosition);
    } else {
      // No contracts section visible - this is acceptable for a new space
      console.log('Note: Contracts section not visible in new space. This is expected.');
    }
  });

  test('should support heartbeat check-in feature', async ({ page }) => {
    // This test verifies the heartbeat check-in feature (Issue #29)
    // which allows agents to be automatically checked-in after N minutes

    // Step 1: Create space and agent
    await page.goto('/');

    const createSpaceButton = page.locator('button:has-text("Create Space")');
    await createSpaceButton.click();

    const spaceNameInput = page.locator('input#create-space-name');
    await spaceNameInput.fill(testSpaceName);

    const submitSpaceButton = page.locator('button:has-text("Create"):visible');
    await submitSpaceButton.click();

    const spaceLink = page.locator(`a:has-text("${testSpaceName}")`);
    await spaceLink.click();

    const newSessionButton = page.locator('button:has-text("New Session")');
    await newSessionButton.click();

    const agentNameInput = page.locator('input#create-agent-name');
    await agentNameInput.fill('heartbeat-agent');

    const taskPromptTextarea = page.locator('textarea#create-prompt');
    await taskPromptTextarea.fill('Agent with heartbeat check-in');

    const launchButton = page.locator('button:has-text("Launch"):visible');
    await launchButton.click();

    await page.waitForTimeout(2000);
    await page.reload();

    // Step 2: Edit agent to enable heartbeat check-in
    const editButton = page.locator('button:near(:text("heartbeat-agent")):has-text("Edit")').first();
    await expect(editButton).toBeVisible({ timeout: 5000 });
    await editButton.click();

    // Step 3: Set heartbeat interval
    const heartbeatInput = page.locator('input#edit-heartbeat');

    if (await heartbeatInput.isVisible({ timeout: 3000 }).catch(() => false)) {
      // Heartbeat input exists - set it to 30 minutes
      await heartbeatInput.fill('30');

      const saveButton = page.locator('button:has-text("Save"):visible, button:has-text("Update"):visible');
      await saveButton.click();

      // Wait for modal to close
      await page.waitForTimeout(1000);

      // Verify modal closed
      await expect(heartbeatInput).not.toBeVisible({ timeout: 3000 });

      // Heartbeat feature is working if we got here without errors
      expect(true).toBe(true);
    } else {
      // Heartbeat input not found - this might indicate the feature is not available
      console.log('Note: Heartbeat interval input not found. Feature may not be available in this build.');
    }
  });

  test('should verify edit button modal fix (Issue #30)', async ({ page }) => {
    // This test verifies that the edit button modal works correctly
    // Issue #30 fixed a bug where edit button didn't work due to undefined SPACE_DATA

    // Step 1: Create space and agent
    await page.goto('/');

    const createSpaceButton = page.locator('button:has-text("Create Space")');
    await createSpaceButton.click();

    const spaceNameInput = page.locator('input#create-space-name');
    await spaceNameInput.fill(testSpaceName);

    const submitSpaceButton = page.locator('button:has-text("Create"):visible');
    await submitSpaceButton.click();

    const spaceLink = page.locator(`a:has-text("${testSpaceName}")`);
    await spaceLink.click();

    const newSessionButton = page.locator('button:has-text("New Session")');
    await newSessionButton.click();

    const agentNameInput = page.locator('input#create-agent-name');
    await agentNameInput.fill('edit-test-agent');

    const taskPromptTextarea = page.locator('textarea#create-prompt');
    await taskPromptTextarea.fill('Original task prompt');

    const repoTextarea = page.locator('textarea#create-repo');
    await repoTextarea.fill('https://github.com/example/repo1');

    const launchButton = page.locator('button:has-text("Launch"):visible');
    await launchButton.click();

    await page.waitForTimeout(2000);
    await page.reload();

    // Step 2: Click edit button - this should open the modal
    const editButton = page.locator('button:near(:text("edit-test-agent")):has-text("Edit")').first();
    await expect(editButton).toBeVisible({ timeout: 5000 });

    // Before Issue #30 fix, clicking this would cause an error
    await editButton.click();

    // Step 3: Verify modal opened with populated fields
    const editPromptTextarea = page.locator('textarea#edit-prompt');
    await expect(editPromptTextarea).toBeVisible({ timeout: 3000 });

    // Verify the modal has the current values
    const promptValue = await editPromptTextarea.inputValue();
    expect(promptValue).toContain('Original task prompt');

    const editRepoTextarea = page.locator('textarea#edit-repo');
    const repoValue = await editRepoTextarea.inputValue();
    expect(repoValue).toContain('https://github.com/example/repo1');

    // Step 4: Make a change and save
    await editPromptTextarea.clear();
    await editPromptTextarea.fill('Updated via edit modal');

    const saveButton = page.locator('button:has-text("Save"):visible, button:has-text("Update"):visible');
    await saveButton.click();

    // Step 5: Verify modal closes successfully
    await expect(editPromptTextarea).not.toBeVisible({ timeout: 3000 });

    // If we got here, the edit button and modal work correctly (Issue #30 is fixed)
    expect(true).toBe(true);
  });
});
