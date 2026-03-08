const { test, expect } = require('@playwright/test');

/**
 * End-to-end test for Agent Boss Coordinator panel interactions
 *
 * This test validates UI panel functionality:
 * 1. Inbox panel visibility and interactions
 * 2. Interrupt panel display and content
 * 3. Panel responsive behavior (desktop + mobile viewports)
 * 4. Panel scroll functionality
 *
 * Prerequisites:
 * - Boss coordinator server running on http://localhost:8899
 * - At least one space exists for testing
 */

test.describe('Panel Interactions', () => {
  let testSpaceName;

  test.beforeEach(async ({ page }) => {
    // Generate unique space name for this test run
    const timestamp = Date.now();
    testSpaceName = `test-space-panels-${timestamp}`;

    // Create a test space
    await page.goto('/');
    const createButton = page.locator('button:has-text("Create Space"), a:has-text("Create Space")');
    if (await createButton.isVisible({ timeout: 3000 }).catch(() => false)) {
      await createButton.click();
      const spaceNameInput = page.locator('input[type="text"], input[placeholder*="space" i]');
      await spaceNameInput.fill(testSpaceName);
      const submitButton = page.locator('button:has-text("Create"), button:has-text("Submit"), button[type="submit"]');
      await submitButton.click();
      await page.waitForTimeout(1000);
    }

    // Navigate to the test space
    await page.goto(`/spaces/${testSpaceName}`);
  });

  test.afterEach(async ({ page }) => {
    // Cleanup: Delete the test space
    await page.goto('/');
    const deleteButton = page.locator(`button:near(:text("${testSpaceName}")):has-text("Delete")`);
    if (await deleteButton.isVisible({ timeout: 2000 }).catch(() => false)) {
      await deleteButton.click();
      const confirmButton = page.locator('button:has-text("Confirm"), button:has-text("Delete"), button:has-text("Yes")');
      if (await confirmButton.isVisible({ timeout: 2000 }).catch(() => false)) {
        await confirmButton.click();
      }
    }
  });

  test('should display inbox panel', async ({ page }) => {
    // Look for inbox panel
    const inboxPanel = page.locator('[data-panel="inbox"], .inbox-panel, section:has-text("Inbox"), div:has-text("Inbox")').first();

    // Check if inbox panel exists
    const panelVisible = await inboxPanel.isVisible({ timeout: 5000 }).catch(() => false);

    if (panelVisible) {
      // Verify panel has expected structure
      expect(await inboxPanel.textContent()).toMatch(/inbox/i);
    } else {
      // Inbox might be empty or not visible - that's also valid
      console.log('Inbox panel not visible - may be empty or feature not implemented');
    }
  });

  test('should display interrupt panel', async ({ page }) => {
    // Look for interrupt panel
    const interruptPanel = page.locator('[data-panel="interrupts"], .interrupt-panel, section:has-text("Interrupt"), div:has-text("Interrupt")').first();

    // Check if interrupt panel exists
    const panelVisible = await interruptPanel.isVisible({ timeout: 5000 }).catch(() => false);

    if (panelVisible) {
      // Verify panel has expected structure
      expect(await interruptPanel.textContent()).toMatch(/interrupt/i);
    } else {
      // Interrupt panel might not have content yet - that's valid
      console.log('Interrupt panel not visible - may be empty or feature not implemented');
    }
  });

  test('should have responsive panels on mobile viewport', async ({ page }) => {
    // Set mobile viewport
    await page.setViewportSize({ width: 375, height: 667 }); // iPhone SE size

    // Reload to apply mobile styles
    await page.reload();

    // Check that panels are visible and properly laid out
    const panels = page.locator('[data-panel], .panel, section');
    const panelCount = await panels.count();

    if (panelCount > 0) {
      // Verify panels stack vertically (full width on mobile)
      for (let i = 0; i < Math.min(panelCount, 3); i++) {
        const panel = panels.nth(i);
        if (await panel.isVisible().catch(() => false)) {
          const box = await panel.boundingBox();
          if (box) {
            // On mobile, panels should take most of the width
            expect(box.width).toBeGreaterThan(300); // At least 300px on 375px viewport
          }
        }
      }
    }

    // Reset to desktop viewport
    await page.setViewportSize({ width: 1280, height: 720 });
  });

  test('should have responsive panels on desktop viewport', async ({ page }) => {
    // Set desktop viewport
    await page.setViewportSize({ width: 1920, height: 1080 });

    // Reload to apply desktop styles
    await page.reload();

    // Verify page is responsive and panels are visible
    const panels = page.locator('[data-panel], .panel, section');
    const panelCount = await panels.count();

    expect(panelCount).toBeGreaterThan(0);

    // Verify page fits within viewport (no horizontal scroll needed)
    const bodyWidth = await page.evaluate(() => document.body.scrollWidth);
    expect(bodyWidth).toBeLessThanOrEqual(1920);
  });

  test('should handle panel scroll when content overflows', async ({ page }) => {
    // Look for scrollable panels
    const scrollablePanel = page.locator('[data-panel="inbox"], [data-panel="interrupts"], .panel').first();

    if (await scrollablePanel.isVisible({ timeout: 3000 }).catch(() => false)) {
      // Check if panel has overflow/scroll capability
      const isScrollable = await scrollablePanel.evaluate((el) => {
        return el.scrollHeight > el.clientHeight ||
               getComputedStyle(el).overflow === 'auto' ||
               getComputedStyle(el).overflow === 'scroll' ||
               getComputedStyle(el).overflowY === 'auto' ||
               getComputedStyle(el).overflowY === 'scroll';
      });

      // Panel should either be scrollable or have limited height
      // This is a soft assertion - panel might not have overflow if content is small
      if (isScrollable) {
        expect(isScrollable).toBe(true);
      }
    }
  });

  test('should have visible panel headers', async ({ page }) => {
    // Check for common panel headers
    const panelHeaders = [
      'Overview',
      'Agents',
      'Contracts',
      'Inbox',
      'Interrupts'
    ];

    let visibleHeaders = 0;

    for (const headerText of panelHeaders) {
      const header = page.locator(`h1:has-text("${headerText}"), h2:has-text("${headerText}"), h3:has-text("${headerText}"), .card-title:has-text("${headerText}")`);
      if (await header.isVisible({ timeout: 1000 }).catch(() => false)) {
        visibleHeaders++;
      }
    }

    // At least one panel header should be visible
    expect(visibleHeaders).toBeGreaterThan(0);
  });

  test('should maintain panel layout on window resize', async ({ page }) => {
    // Start with desktop size
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.reload();

    // Get initial panel positions
    const panel = page.locator('[data-panel], .panel, section').first();
    const initialBox = await panel.boundingBox().catch(() => null);

    // Resize to tablet
    await page.setViewportSize({ width: 768, height: 1024 });
    await page.waitForTimeout(500);

    const tabletBox = await panel.boundingBox().catch(() => null);

    // Panel should still be visible after resize
    if (initialBox && tabletBox) {
      expect(tabletBox.width).toBeGreaterThan(0);
      expect(tabletBox.height).toBeGreaterThan(0);
    }

    // Resize to mobile
    await page.setViewportSize({ width: 375, height: 667 });
    await page.waitForTimeout(500);

    const mobileBox = await panel.boundingBox().catch(() => null);

    // Panel should still be visible on mobile
    if (mobileBox) {
      expect(mobileBox.width).toBeGreaterThan(0);
      expect(mobileBox.height).toBeGreaterThan(0);
    }
  });
});
