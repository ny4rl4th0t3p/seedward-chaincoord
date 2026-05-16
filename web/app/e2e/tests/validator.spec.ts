import { test, expect } from '../fixtures/test';
import { validator } from '../fixtures/keypairs';
import { createLaunch } from '../helpers/launch';
import { loginAs } from '../helpers/auth';
import { installValidatorWalletStub } from '../helpers/wallet-stub';

// K.4 (validator single-actor) + K.5 (multi-actor full flow)

// ── K.4 Validator panel (single-actor) ────────────────────────────────────────

test('K.4.1val submit join request shows pending status', async ({ browser }) => {
  // Coordinator context — create and open a launch.
  const coordCtx = await browser.newContext();
  const coordPage = await coordCtx.newPage();
  const launchId = await createLaunch(coordPage, { chainName: 'k4val1', chainId: 'k4val1-1', bech32Prefix: 'k4v1' });

  // Upload initial genesis ref (required before OpenWindow — server auto-publishes from DRAFT+SHA).
  await coordPage.getByPlaceholder('https://files.example.com/genesis.json').fill('https://example.com/genesis.json');
  await coordPage.getByPlaceholder('64-character hex digest').fill('a'.repeat(64));
  await coordPage.getByRole('button', { name: /submit genesis reference/i }).click();
  await expect(coordPage.getByText(/genesis reference saved/i)).toBeVisible({ timeout: 10_000 });

  await coordPage.getByRole('button', { name: /open application window/i }).click();
  await expect(coordPage.getByText(/WINDOW_OPEN/i).or(coordPage.getByText(/\bopen\b/i)).first()).toBeVisible({ timeout: 10_000 });

  // Validator context — add chain stub (chain hint fetched from server), sign in, submit join request.
  const valCtx = await browser.newContext();
  const valPage = await valCtx.newPage();

  // Pre-load auth via sessionStorage (bypasses unreliable headless interchain-kit connect flow).
  // installValidatorWalletStub must precede loginAs so window.keplr is available for
  // join-request signing via the keplr fallback path in buildSignedAction.
  await installValidatorWalletStub(valPage, validator(), 'k4v1');
  await loginAs(valPage, validator(), { bech32Prefix: 'k4v1', navigateTo: `/launch/${launchId}` });

  // Fill and submit join request.
  await valPage.getByPlaceholder(/1\.2\.3\.4/i).fill('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa@1.2.3.4:26656');
  await valPage.getByPlaceholder(/MsgCreateValidator/).fill('{"body":{"messages":[{"@type":"/cosmos.staking.v1beta1.MsgCreateValidator","description":{"moniker":"e2e-validator"},"pubkey":{"@type":"/cosmos.crypto.ed25519.PubKey","key":"f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="},"value":{"denom":"uk4val1","amount":"1000000"}}]},"auth_info":{},"signatures":[]}');
  await valPage.getByRole('button', { name: /submit join request/i }).click();

  await expect(valPage.getByText(/pending/i)).toBeVisible({ timeout: 10_000 });
  await expect(valPage.getByRole('button', { name: /submit join request/i })).not.toBeVisible();

  await coordCtx.close();
  await valCtx.close();
});

// ── K.5 Full coordinator + validator flow (multi-actor) ────────────────────────

test('K.5 full launch flow: create → open → join → approve', async ({ browser }) => {
  test.setTimeout(90_000);
  const CHAIN_PREFIX = 'k5';

  // ── Step 1: coordinator creates and opens launch ────────────────────────────
  const coordCtx = await browser.newContext();
  const coordPage = await coordCtx.newPage();
  const launchId = await createLaunch(coordPage, {
    chainName: 'k5chain',
    chainId: 'k5chain-1',
    bech32Prefix: CHAIN_PREFIX,
    denom: 'uk5',
  });

  // Upload initial genesis ref (required before OpenWindow — server auto-publishes from DRAFT+SHA).
  await coordPage.getByPlaceholder('https://files.example.com/genesis.json').fill('https://example.com/genesis.json');
  await coordPage.getByPlaceholder('64-character hex digest').fill('a'.repeat(64));
  await coordPage.getByRole('button', { name: /submit genesis reference/i }).click();
  await expect(coordPage.getByText(/genesis reference saved/i)).toBeVisible({ timeout: 10_000 });

  await coordPage.getByRole('button', { name: /open application window/i }).click();
  await expect(coordPage.getByText(/WINDOW_OPEN/i).or(coordPage.getByText(/\bopen\b/i)).first()).toBeVisible({ timeout: 10_000 });
  await coordPage.reload();

  // ── Step 2: validator joins ─────────────────────────────────────────────────
  const valCtx = await browser.newContext();
  const valPage = await valCtx.newPage();

  // Pre-load auth via sessionStorage — same reliable approach as loginAs for coordinator.
  await installValidatorWalletStub(valPage, validator(), CHAIN_PREFIX);
  await loginAs(valPage, validator(), { bech32Prefix: CHAIN_PREFIX, navigateTo: `/launch/${launchId}` });

  await valPage.getByPlaceholder(/1\.2\.3\.4/i).fill('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa@1.2.3.4:26656');
  await valPage.getByPlaceholder(/MsgCreateValidator/).fill('{"body":{"messages":[{"@type":"/cosmos.staking.v1beta1.MsgCreateValidator","description":{"moniker":"e2e-validator"},"pubkey":{"@type":"/cosmos.crypto.ed25519.PubKey","key":"f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="},"value":{"denom":"uk5","amount":"1000000"}}]},"auth_info":{},"signatures":[]}');
  await valPage.getByRole('button', { name: /submit join request/i }).click();
  await expect(valPage.getByText(/pending/i)).toBeVisible({ timeout: 10_000 });

  // ── Step 3: coordinator approves ────────────────────────────────────────────
  await coordPage.reload();

  // Raise an APPROVE_VALIDATOR proposal.
  const approveBtn = coordPage.getByRole('button', { name: /approve/i }).first();
  await approveBtn.waitFor({ state: 'visible', timeout: 10_000 });
  await approveBtn.click();
  await coordPage.getByRole('button', { name: /sign & raise/i }).click();

  // Sign the proposal (1-of-1 threshold → auto-executes).
  await expect(coordPage.getByText(/executed/i)).toBeVisible({ timeout: 15_000 });

  // ── Step 4: validator sees approved banner ──────────────────────────────────
  await valPage.reload();
  await expect(valPage.getByText(/your join request has been approved/i)).toBeVisible({ timeout: 15_000 });
  await expect(valPage.getByRole('button', { name: /load peers/i })).toBeVisible();

  // ── Step 5: coordinator can download gentxs ─────────────────────────────────
  // (Initial genesis was uploaded before opening window; readiness confirmation
  // requires WINDOW_CLOSED + final genesis, which is not part of this test.)
  await coordPage.reload();
  await expect(coordPage.getByRole('button', { name: /download gentxs/i })).toBeVisible();

  await coordCtx.close();
  await valCtx.close();
});
