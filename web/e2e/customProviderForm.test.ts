/**
 * Issue #234: typing in the custom-provider edit dialog lost every unsaved
 * field after a few seconds.
 *
 * The dashboard polls /api/provider-nodes every 10s (App.svelte) and replaces
 * the array wholesale, so `selectedNode` reaches EditCompatibleNodeModal as a
 * new object with identical contents on every tick. The seed effect keyed on
 * that identity, so it re-ran mid-typing and restored the stored values.
 *
 * Driven as the report described: type, wait past a poll tick, read the field
 * back. Asserting "the dialog opened" would pass while the fields were being
 * clobbered on a timer.
 */

import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import type { Page } from '@playwright/test'

import { startDashboard, type Dashboard } from './harness'

/** Covers the dashboard's 10-second poll and the preceding form transitions. */
const SLOW = 60_000

let app: Dashboard
let page: Page

beforeAll(async () => {
  app = await startDashboard(20417)
  page = app.page
}, SLOW)

afterAll(async () => {
  await app.stop()
})

/**
 * The input a field label sits above.
 *
 * Input.svelte emits the label and the input as siblings inside one wrapper and
 * gives the input no id, so a `getByLabel()` pairing finds nothing and a
 * `div:has(label)` filter matches every ancestor up to the dialog. Walking to
 * the label's own parent and taking that wrapper's single control is the
 * narrowest handle the markup supports.
 */
function field(page: Page, label: string) {
  return page
    .locator('label')
    .filter({ hasText: new RegExp(`^${label}`) })
    .locator('xpath=..')
    .locator('input, textarea, select')
    .first()
}

describe('custom provider edit dialog (#234)', () => {
  beforeAll(async () => {
    await page.getByRole('link', { name: 'Providers' }).click()

    // A real stored node, created through the UI, so the case runs against a
    // row the dashboard actually polls back rather than a faked prop.
    await page.getByRole('button', { name: 'Add OpenAI Compatible', exact: true }).click()
    await page.getByRole('radio', { name: /OpenAI/ }).first().waitFor()

    await field(page, 'Name').fill('Poll Survivor')
    await field(page, 'Prefix').fill('pollsurvivor')
    await field(page, 'Base URL').fill('https://api.example.com/v1')
    await page.getByRole('button', { name: 'Create', exact: true }).click()

    await page.getByText('OpenAI Compatible Details').waitFor()
    await page.getByRole('button', { name: 'Edit' }).click()
    await page.getByText('Edit OpenAI Compatible').waitFor()
  }, SLOW)

  test(
    'typed fields survive the dashboard poll',
    async () => {
      const name = field(page, 'Name')
      const prefix = field(page, 'Prefix')
      const baseUrl = field(page, 'Base URL')

      // The stored values, so the assertions below prove the edits survived
      // rather than that the fields simply never changed.
      expect(await name.inputValue()).toBe('Poll Survivor')
      expect(await baseUrl.inputValue()).toBe('https://api.example.com/v1')

      await name.fill('Poll Survivor Edited')
      await prefix.fill('pollsurvivor2')
      await baseUrl.fill('https://api.example.com/v2')

      // Await the actual provider-node poll rather than guessing its timing.
      const polledNodes = await page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/provider-nodes' &&
          response.request().method() === 'GET' &&
          response.status() === 200,
        { timeout: 20_000 },
      )
      await polledNodes.finished()
      // The browser task after delivery lets Svelte flush the poll's state update.
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())))

      expect(await name.inputValue()).toBe('Poll Survivor Edited')
      expect(await prefix.inputValue()).toBe('pollsurvivor2')
      expect(await baseUrl.inputValue()).toBe('https://api.example.com/v2')
    },
    SLOW,
  )

  test(
    'the edits are still there when the dialog is reopened',
    async () => {
      // A close/reopen must reseed from the stored row, not carry the previous
      // draft over — the half of the contract that keeps this fix from being a
      // stale-form bug.
      await page.getByText('Edit OpenAI Compatible').waitFor()

      await page.keyboard.press('Escape')
      await page.getByText('Edit OpenAI Compatible').waitFor({ state: 'hidden' })

      await page.getByRole('button', { name: 'Edit' }).click()
      await page.getByText('Edit OpenAI Compatible').waitFor()

      expect(await field(page, 'Name').inputValue()).toBe('Poll Survivor')
    },
    SLOW,
  )
})