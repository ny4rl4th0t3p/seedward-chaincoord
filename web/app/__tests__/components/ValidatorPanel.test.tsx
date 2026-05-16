import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { ValidatorPanel } from '@/components/ValidatorPanel';
import { buildSignedAction } from '@/utils/signedAction';
import type { Launch, Dashboard } from '@/types';

// ── ESM shims ────────────────────────────────────────────────────────────────

jest.mock('@interchain-kit/core', () => ({ CosmosWallet: class CosmosWallet {} }));
jest.mock('@interchain-kit/react/store/stateful-wallet', () => ({
  StatefulWallet: class StatefulWallet {},
}));

// ── Lightweight UI stubs ─────────────────────────────────────────────────────
// Avoids loading the full interchain-ui component tree in jsdom.

jest.mock('@interchain-ui/react', () => ({
  Box: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  Text: ({ children }: { children?: React.ReactNode }) => <span>{children}</span>,
}));

jest.mock('@/components', () => ({
  Button: ({
    children,
    onClick,
    isLoading,
    isDisabled,
  }: {
    children?: React.ReactNode;
    onClick?: () => void;
    isLoading?: boolean;
    isDisabled?: boolean;
  }) => (
    <button onClick={onClick} disabled={isDisabled ?? isLoading}>
      {isLoading ? children : children}
    </button>
  ),
}));

// ── buildSignedAction mock ───────────────────────────────────────────────────
// Payload shape is verified separately in signedAction.test.ts.

jest.mock('@/utils/signedAction', () => ({
  buildSignedAction: jest.fn(),
}));

const mockSign = buildSignedAction as jest.MockedFunction<typeof buildSignedAction>;

// ── Fixtures ─────────────────────────────────────────────────────────────────

const LAUNCH_ID = 'aaaaaaaa-0000-0000-0000-000000000001';
const HINT = {
  chain_id: 'mychain-1',
  chain_name: 'mychain',
  bech32_prefix: 'cosmos',
  denom: 'uatom',
};
const ADDRESS = 'cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu';
const MOCK_WALLET = {} as any; // not used — buildSignedAction is mocked

const BASE_RECORD = {
  chain_id: 'mychain-1',
  chain_name: 'mychain',
  bech32_prefix: 'cosmos',
  binary_name: 'gaiad',
  binary_version: 'v15.0.0',
  binary_sha256: 'abc123',
  repo_url: '',
  repo_commit: '',
  denom: 'uatom',
  min_self_delegation: '1',
  max_commission_rate: '0.2',
  max_commission_change_rate: '0.01',
  gentx_deadline: '2026-06-01T00:00:00Z',
  application_window_open: '2026-05-01T00:00:00Z',
  min_validator_count: 4,
};

function makeLaunch(overrides?: Partial<Launch>): Launch {
  return {
    id: LAUNCH_ID,
    record: BASE_RECORD,
    launch_type: 'testnet',
    visibility: 'public',
    status: 'open',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

function makeDashboard(overrides?: Partial<Dashboard>): Dashboard {
  return {
    launch_id: LAUNCH_ID,
    chain_id: 'mychain-1',
    status: 'open',
    total_approved: 0,
    confirmed_ready: 0,
    voting_power_confirmed: 0,
    threshold_status: 'below',
    validators: [],
    ...overrides,
  };
}

function defaultProps(overrides?: {
  launch?: Partial<Launch>;
  dashboard?: Partial<Dashboard> | null;
  authFetch?: jest.Mock;
}) {
  return {
    launchId: LAUNCH_ID,
    hint: HINT,
    address: ADDRESS,
    wallet: MOCK_WALLET,
    signingChainId: HINT.chain_id,
    launch: makeLaunch(overrides?.launch),
    dashboard: overrides?.dashboard === null ? null : makeDashboard(overrides?.dashboard ?? {}),
    authFetch: overrides?.authFetch ?? jest.fn(),
  };
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function mockSignedResult(extra?: object) {
  mockSign.mockResolvedValue({
    operator_address: ADDRESS,
    timestamp: '2026-01-01T00:00:00Z',
    nonce: 'test-nonce',
    pubkey_b64: 'testpubkey==',
    signature: 'testsig==',
    ...extra,
  } as any);
}

const VALID_GENTX = '{"body":{"messages":[{"@type":"/cosmos.staking.v1beta1.MsgCreateValidator"}]}}';

// Fill out the join form with valid data and click submit.
async function fillAndSubmitJoinForm() {
  fireEvent.change(screen.getByPlaceholderText(/\{"body"/), {
    target: { value: VALID_GENTX },
  });
  fireEvent.change(screen.getByPlaceholderText(/1\.2\.3\.4:26656/), {
    target: { value: '5.6.7.8:26656' },
  });
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /Submit Join Request/ }));
  });
}

// ── Routing tests ─────────────────────────────────────────────────────────────

describe('ValidatorPanel — routing', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockSignedResult();
  });

  it('shows join form when validator is not in dashboard validators', () => {
    render(<ValidatorPanel {...defaultProps()} />);
    expect(screen.getByRole('button', { name: /Submit Join Request/ })).toBeInTheDocument();
  });

  it('shows approved banner when validator address is in dashboard.validators', () => {
    const dashboard = makeDashboard({
      validators: [
        {
          join_request_id: 'req-1',
          operator_address: ADDRESS,
          moniker: 'mynode',
          voting_power_pct: 0.1,
          is_ready: false,
        },
      ],
    });
    render(<ValidatorPanel {...defaultProps()} dashboard={dashboard} />);
    expect(screen.getByText(/Your join request has been approved/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Submit Join Request/ })).not.toBeInTheDocument();
  });

  it('shows genesis section only when launch has final_genesis_sha256 and is approved', () => {
    const dashboard = makeDashboard({
      validators: [{ join_request_id: 'r1', operator_address: ADDRESS, moniker: '', voting_power_pct: 0, is_ready: false }],
    });
    const launch = makeLaunch({ final_genesis_sha256: 'abc123deadbeef' });

    render(<ValidatorPanel {...defaultProps()} launch={launch} dashboard={dashboard} />);
    expect(screen.getByRole('button', { name: /Download genesis/ })).toBeInTheDocument();
  });

  it('hides genesis section when launch has no final_genesis_sha256', () => {
    const dashboard = makeDashboard({
      validators: [{ join_request_id: 'r1', operator_address: ADDRESS, moniker: '', voting_power_pct: 0, is_ready: false }],
    });
    render(<ValidatorPanel {...defaultProps()} launch={makeLaunch()} dashboard={dashboard} />);
    expect(screen.queryByRole('button', { name: /Download genesis/ })).not.toBeInTheDocument();
  });

  it('shows readiness form when approved, not ready, and genesis exists', () => {
    const dashboard = makeDashboard({
      validators: [{ join_request_id: 'r1', operator_address: ADDRESS, moniker: '', voting_power_pct: 0, is_ready: false }],
    });
    const launch = makeLaunch({ final_genesis_sha256: 'abc123deadbeef' });

    render(<ValidatorPanel {...defaultProps()} launch={launch} dashboard={dashboard} />);
    expect(screen.getByRole('button', { name: /Confirm Readiness/ })).toBeInTheDocument();
  });

  it('shows confirmed banner instead of readiness form when is_ready=true', () => {
    const dashboard = makeDashboard({
      validators: [{ join_request_id: 'r1', operator_address: ADDRESS, moniker: '', voting_power_pct: 0, is_ready: true }],
    });
    const launch = makeLaunch({ final_genesis_sha256: 'abc123deadbeef' });

    render(<ValidatorPanel {...defaultProps()} launch={launch} dashboard={dashboard} />);
    expect(screen.queryByRole('button', { name: /Confirm Readiness/ })).not.toBeInTheDocument();
    expect(screen.getByText(/Readiness confirmed/)).toBeInTheDocument();
  });
});

// ── JoinSection tests ─────────────────────────────────────────────────────────

describe('ValidatorPanel — JoinSection (H.3 / H.4)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockSignedResult();
  });

  it('submit shows error for missing peer address', async () => {
    render(<ValidatorPanel {...defaultProps()} />);
    // Fill gentx but leave peer address empty
    fireEvent.change(screen.getByPlaceholderText(/\{"body"/), {
      target: { value: VALID_GENTX },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Submit Join Request/ }));
    });
    expect(screen.getByText(/Peer address is required/)).toBeInTheDocument();
  });

  it('shows validation error for invalid gentx JSON', async () => {
    render(<ValidatorPanel {...defaultProps()} />);
    fireEvent.change(screen.getByPlaceholderText(/1\.2\.3\.4:26656/), {
      target: { value: '5.6.7.8:26656' },
    });
    fireEvent.change(screen.getByPlaceholderText(/\{"body"/), {
      target: { value: 'not-valid-json' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Submit Join Request/ }));
    });
    expect(screen.getByText(/gentx must be valid JSON/)).toBeInTheDocument();
  });

  it('calls buildSignedAction with the correct payload shape', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ id: 'req-123', status: 'pending', launch_id: LAUNCH_ID, operator_address: ADDRESS }),
    });
    render(<ValidatorPanel {...defaultProps({ authFetch })} />);
    await fillAndSubmitJoinForm();

    expect(mockSign).toHaveBeenCalledTimes(1);
    const [payload, , chainId, addr] = mockSign.mock.calls[0];
    expect(chainId).toBe('mychain-1');
    expect(addr).toBe(ADDRESS);
    expect(payload).toMatchObject({
      chain_id: 'mychain-1',
      operator_address: ADDRESS,
      peer_address: '5.6.7.8:26656',
    });
    expect(payload).not.toHaveProperty('consensus_pubkey');
    // gentx must be the parsed object, not a raw string
    expect(typeof payload.gentx).toBe('object');
  });

  it('calls POST /launch/{id}/join with the signed body', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ id: 'req-123', status: 'pending', launch_id: LAUNCH_ID, operator_address: ADDRESS }),
    });
    render(<ValidatorPanel {...defaultProps({ authFetch })} />);
    await fillAndSubmitJoinForm();

    expect(authFetch).toHaveBeenCalledWith(
      `/launch/${LAUNCH_ID}/join`,
      expect.objectContaining({ method: 'POST' }),
    );
    const body = JSON.parse(authFetch.mock.calls[0][1].body);
    expect(body.nonce).toBe('test-nonce');
    expect(body.signature).toBe('testsig==');
  });

  it('shows status card with join request ID after successful submit', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ id: 'req-abc-123', status: 'pending', launch_id: LAUNCH_ID, operator_address: ADDRESS }),
    });
    render(<ValidatorPanel {...defaultProps({ authFetch })} />);
    await fillAndSubmitJoinForm();

    await waitFor(() => {
      expect(screen.getByText(/req-abc-123/)).toBeInTheDocument();
    });
    expect(screen.getByText(/PENDING/)).toBeInTheDocument();
    // Form should be gone
    expect(screen.queryByRole('button', { name: /Submit Join Request/ })).not.toBeInTheDocument();
  });

  it('shows conflict message on 409 response', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({ message: 'duplicate join request' }),
    });
    render(<ValidatorPanel {...defaultProps({ authFetch })} />);
    await fillAndSubmitJoinForm();

    await waitFor(() => {
      expect(screen.getByText(/already have a pending join request/)).toBeInTheDocument();
    });
  });

  it('shows server error message on non-409 failure', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: false,
      status: 400,
      json: async () => ({ message: 'invalid peer address' }),
    });
    render(<ValidatorPanel {...defaultProps({ authFetch })} />);
    await fillAndSubmitJoinForm();

    await waitFor(() => {
      expect(screen.getByText('invalid peer address')).toBeInTheDocument();
    });
  });

  it('polls GET /launch/{id}/join/{req_id} after successful submit', async () => {
    const authFetch = jest.fn()
      // POST /join
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ id: 'req-poll-1', status: 'pending', launch_id: LAUNCH_ID, operator_address: ADDRESS }),
      })
      // first poll
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ id: 'req-poll-1', status: 'approved', launch_id: LAUNCH_ID, operator_address: ADDRESS }),
      });

    render(<ValidatorPanel {...defaultProps({ authFetch })} />);
    await fillAndSubmitJoinForm();

    await waitFor(() => {
      expect(authFetch).toHaveBeenCalledWith(`/launch/${LAUNCH_ID}/join/req-poll-1`);
    });
    await waitFor(() => {
      expect(screen.getByText(/APPROVED/)).toBeInTheDocument();
    });
  });
});

// ── GenesisSection tests ──────────────────────────────────────────────────────

describe('ValidatorPanel — GenesisSection (H.5)', () => {
  const GENESIS_SHA256 = 'abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890';

  let anchorClickSpy: jest.SpyInstance;

  beforeEach(() => {
    jest.clearAllMocks();
    // SHA-256 stub: produce a hex that matches GENESIS_SHA256 by controlling the digest output
    const hashBytes = new Uint8Array(
      GENESIS_SHA256.match(/.{2}/g)!.map((h) => parseInt(h, 16)),
    );
    Object.defineProperty(global, 'crypto', {
      value: {
        subtle: {
          digest: jest.fn().mockResolvedValue(hashBytes.buffer),
        },
        randomUUID: crypto.randomUUID.bind(crypto),
      },
      writable: true,
    });
    global.URL.createObjectURL = jest.fn().mockReturnValue('blob:fake');
    global.URL.revokeObjectURL = jest.fn();
    // Prevent jsdom "not implemented: navigation" from the synthetic <a>.click()
    anchorClickSpy = jest.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  });

  afterEach(() => {
    anchorClickSpy.mockRestore();
  });

  function approvedProps(authFetch: jest.Mock) {
    const dashboard = makeDashboard({
      validators: [{ join_request_id: 'r1', operator_address: ADDRESS, moniker: '', voting_power_pct: 0, is_ready: false }],
    });
    return defaultProps({
      launch: { final_genesis_sha256: GENESIS_SHA256 },
      dashboard,
      authFetch,
    });
  }

  it('renders expected SHA-256 hash', () => {
    render(<ValidatorPanel {...approvedProps(jest.fn())} />);
    expect(screen.getByText(GENESIS_SHA256)).toBeInTheDocument();
  });

  it('calls GET /launch/{id}/genesis on download click', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: true,
      arrayBuffer: async () => new ArrayBuffer(8),
    });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Download genesis/ }));
    });

    expect(authFetch).toHaveBeenCalledWith(`/launch/${LAUNCH_ID}/genesis`);
  });

  it('shows hash match confirmation after successful download', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: true,
      arrayBuffer: async () => new ArrayBuffer(32),
    });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Download genesis/ }));
    });

    await waitFor(() => {
      expect(screen.getByText(/✓ match/)).toBeInTheDocument();
    });
  });

  it('shows mismatch warning when hashes differ', async () => {
    // digest returns all-zero bytes → different from GENESIS_SHA256
    (crypto.subtle.digest as jest.Mock).mockResolvedValue(new ArrayBuffer(32));

    const authFetch = jest.fn().mockResolvedValue({
      ok: true,
      arrayBuffer: async () => new ArrayBuffer(32),
    });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Download genesis/ }));
    });

    await waitFor(() => {
      expect(screen.getByText(/✗ mismatch/)).toBeInTheDocument();
    });
  });

  it('shows error message when genesis fetch fails', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: false,
      status: 404,
      json: async () => ({ message: 'genesis not found' }),
    });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Download genesis/ }));
    });

    await waitFor(() => {
      expect(screen.getByText('genesis not found')).toBeInTheDocument();
    });
  });
});

// ── ReadinessSection tests ────────────────────────────────────────────────────

describe('ValidatorPanel — ReadinessSection (H.6)', () => {
  const GENESIS_SHA256 = 'deadbeef00000000deadbeef00000000deadbeef00000000deadbeef00000000';

  beforeEach(() => {
    jest.clearAllMocks();
    mockSignedResult();
  });

  function approvedProps(authFetch: jest.Mock) {
    const dashboard = makeDashboard({
      validators: [{ join_request_id: 'r1', operator_address: ADDRESS, moniker: '', voting_power_pct: 0, is_ready: false }],
    });
    return defaultProps({
      launch: { final_genesis_sha256: GENESIS_SHA256 },
      dashboard,
      authFetch,
    });
  }

  it('pre-fills the genesis hash field', () => {
    render(<ValidatorPanel {...approvedProps(jest.fn())} />);
    const inputs = screen.getAllByDisplayValue(GENESIS_SHA256);
    expect(inputs.length).toBeGreaterThan(0);
  });

  it('shows validation error when binary hash is empty', async () => {
    render(<ValidatorPanel {...approvedProps(jest.fn())} />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Confirm Readiness/ }));
    });
    expect(screen.getByText(/Binary SHA-256 hash is required/)).toBeInTheDocument();
  });

  it('calls buildSignedAction with genesis and binary hashes', async () => {
    const authFetch = jest.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    fireEvent.change(screen.getByPlaceholderText(/sha256sum output/), {
      target: { value: 'binaryhash1234' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Confirm Readiness/ }));
    });

    expect(mockSign).toHaveBeenCalledTimes(1);
    const [payload] = mockSign.mock.calls[0];
    expect(payload).toMatchObject({
      genesis_hash_confirmed: GENESIS_SHA256,
      binary_hash_confirmed: 'binaryhash1234',
      operator_address: ADDRESS,
    });
  });

  it('calls POST /launch/{id}/ready', async () => {
    const authFetch = jest.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    fireEvent.change(screen.getByPlaceholderText(/sha256sum output/), {
      target: { value: 'binaryhash1234' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Confirm Readiness/ }));
    });

    expect(authFetch).toHaveBeenCalledWith(
      `/launch/${LAUNCH_ID}/ready`,
      expect.objectContaining({ method: 'POST' }),
    );
  });

  it('shows confirmed state after successful submission', async () => {
    const authFetch = jest.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    fireEvent.change(screen.getByPlaceholderText(/sha256sum output/), {
      target: { value: 'binaryhash1234' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Confirm Readiness/ }));
    });

    await waitFor(() => {
      expect(screen.getByText(/Readiness confirmed — the coordinator/)).toBeInTheDocument();
    });
    expect(screen.queryByRole('button', { name: /Confirm Readiness/ })).not.toBeInTheDocument();
  });

  it('shows server error message on failure', async () => {
    const authFetch = jest.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({ message: 'already confirmed readiness' }),
    });
    render(<ValidatorPanel {...approvedProps(authFetch)} />);

    fireEvent.change(screen.getByPlaceholderText(/sha256sum output/), {
      target: { value: 'binaryhash1234' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Confirm Readiness/ }));
    });

    await waitFor(() => {
      expect(screen.getByText('already confirmed readiness')).toBeInTheDocument();
    });
  });
});