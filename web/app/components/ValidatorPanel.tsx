import { useState, useEffect, useCallback } from 'react';
import { Box, Text } from '@interchain-ui/react';
import type { StatefulWallet } from '@interchain-kit/react/store/stateful-wallet';
import { Button } from '@/components';
import { buildSignedAction } from '@/utils/signedAction';
import type { ChainHint } from '@/utils/chainSuggestion';
import type { Launch, Dashboard, JoinRequest } from '@/types';

// ── Helpers ───────────────────────────────────────────────────────────────────

async function computeSha256Hex(buffer: ArrayBuffer): Promise<string> {
  const hash = await crypto.subtle.digest('SHA-256', buffer);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

// ── Sub-panel UI primitives ───────────────────────────────────────────────────

function PanelCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Box borderRadius="8px" border="1px solid" borderColor="$divider" p="20px">
      <Text fontSize="$md" fontWeight="$semibold" attributes={{ mb: '12px' }}>
        {title}
      </Text>
      <Box display="flex" flexDirection="column" gap="8px">
        {children}
      </Box>
    </Box>
  );
}

function FieldLabel({ children }: { children: React.ReactNode }) {
  return (
    <Text fontSize="$xs" color="$textSecondary" attributes={{ mb: '2px' }}>
      {children}
    </Text>
  );
}

// Minimal styled input wrapper — @interchain-ui/react doesn't expose a plain
// text Input that matches the rest of the palette, so we inline a basic style.
function TextInput({
  value,
  onChange,
  placeholder,
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  disabled?: boolean;
}) {
  return (
    <input
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      disabled={disabled}
      style={{
        width: '100%',
        padding: '8px 12px',
        borderRadius: '6px',
        border: '1px solid var(--chakra-colors-divider, #e2e8f0)',
        background: 'transparent',
        color: 'inherit',
        fontSize: '14px',
        fontFamily: 'inherit',
        boxSizing: 'border-box',
      }}
    />
  );
}

function TextArea({
  value,
  onChange,
  placeholder,
  disabled,
  rows,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  disabled?: boolean;
  rows?: number;
}) {
  return (
    <textarea
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      disabled={disabled}
      rows={rows ?? 6}
      style={{
        width: '100%',
        padding: '8px 12px',
        borderRadius: '6px',
        border: '1px solid var(--chakra-colors-divider, #e2e8f0)',
        background: 'transparent',
        color: 'inherit',
        fontSize: '13px',
        fontFamily: 'monospace',
        resize: 'vertical',
        boxSizing: 'border-box',
      }}
    />
  );
}

// ── H.3 / H.4 — Join Section ──────────────────────────────────────────────────

interface JoinSectionProps {
  launchId: string;
  hint: ChainHint;
  address: string;
  wallet: StatefulWallet;
  signingChainId: string;
  isApproved: boolean;
  authFetch: (url: string, init?: RequestInit) => Promise<Response>;
}

function JoinSection({ launchId, hint, address, wallet, signingChainId, isApproved, authFetch }: JoinSectionProps) {
  // Form fields — all hooks must come before any conditional returns
  const [gentxRaw, setGentxRaw] = useState('');
  const [peerAddress, setPeerAddress] = useState('');
  const [rpcEndpoint, setRpcEndpoint] = useState('');
  const [memo, setMemo] = useState('');

  // State
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [joinRequestId, setJoinRequestId] = useState<string | null>(null);
  const [joinRequest, setJoinRequest] = useState<JoinRequest | null>(null);
  const [pollError, setPollError] = useState<string | null>(null);

  // H.4: poll for join request status once we have an ID
  useEffect(() => {
    if (!joinRequestId) return;

    const poll = async () => {
      try {
        const r = await authFetch(`/launch/${launchId}/join/${joinRequestId}`);
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          setPollError((body as { message?: string }).message ?? 'Failed to fetch status');
          return;
        }
        const jr: JoinRequest = await r.json();
        setJoinRequest(jr);
      } catch (err) {
        setPollError(err instanceof Error ? err.message : 'Network error');
      }
    };

    poll();
    const id = setInterval(poll, 15_000);
    return () => clearInterval(id);
  }, [joinRequestId, launchId, authFetch]);

  // If validator is already approved via dashboard, just show the status card.
  if (isApproved) {
    return (
      <PanelCard title="Join Request">
        <Text fontSize="$sm" color="$textSuccess">
          Your join request has been approved.
        </Text>
      </PanelCard>
    );
  }

  // H.4: show status after submit
  if (joinRequestId) {
    const statusColor: Record<string, string> = {
      pending: '$textSecondary',
      approved: '$textSuccess',
      rejected: '$textDanger',
    };
    const status = joinRequest?.status ?? 'pending';
    return (
      <PanelCard title="Join Request Status">
        <Box display="flex" gap="8px" alignItems="center">
          <Text fontSize="$sm" color="$textSecondary">Status:</Text>
          <Text fontSize="$sm" fontWeight="$semibold" color={statusColor[status] ?? '$textSecondary'}>
            {status.toUpperCase()}
          </Text>
        </Box>
        {joinRequest?.rejection_reason && (
          <Text fontSize="$sm" color="$textDanger">
            Reason: {joinRequest.rejection_reason}
          </Text>
        )}
        {pollError && (
          <Text fontSize="$xs" color="$textDanger">{pollError}</Text>
        )}
        <Text fontSize="$xs" color="$textSecondary">
          Request ID: {joinRequestId}
        </Text>
        <Text fontSize="$xs" color="$textSecondary">
          Polling every 15 s — refresh this page to check again if you reopen the browser.
        </Text>
      </PanelCard>
    );
  }

  // H.3: join request form
  const handleGentxFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const text = await file.text();
    setGentxRaw(text);
  };

  const handleSubmit = async () => {
    setSubmitError(null);

    if (!peerAddress.trim()) {
      setSubmitError('Peer address is required.');
      return;
    }

    let gentxParsed: unknown;
    try {
      gentxParsed = JSON.parse(gentxRaw);
    } catch {
      setSubmitError('gentx must be valid JSON.');
      return;
    }

    setIsSubmitting(true);
    try {
      const payload = {
        chain_id: hint.chain_id,
        gentx: gentxParsed,
        memo: memo.trim(),
        operator_address: address,
        peer_address: peerAddress.trim(),
        rpc_endpoint: rpcEndpoint.trim(),
      };

      const signed = await buildSignedAction(payload, wallet, signingChainId, address);

      const r = await authFetch(`/launch/${launchId}/join`, {
        method: 'POST',
        body: JSON.stringify(signed),
      });

      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        const msg = (body as { message?: string }).message;
        if (r.status === 409) {
          setSubmitError(
            'You already have a pending join request for this launch. Refresh the page — if you see the join form again, the request ID was lost (browser tab closed). Contact the coordinator with your operator address to check status.',
          );
        } else {
          setSubmitError(msg ?? `Server returned ${r.status}`);
        }
        return;
      }

      const jr: JoinRequest = await r.json();
      setJoinRequestId(jr.id);
      setJoinRequest(jr);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : 'Unexpected error');
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <PanelCard title="Submit Join Request">
      <Box display="flex" flexDirection="column" gap="12px">
        <Box>
          <FieldLabel>gentx JSON — upload file or paste below *</FieldLabel>
          <input
            type="file"
            accept=".json"
            onChange={handleGentxFile}
            disabled={isSubmitting}
            style={{ marginBottom: '8px', fontSize: '13px' }}
          />
          <TextArea
            value={gentxRaw}
            onChange={setGentxRaw}
            placeholder='{"body":{"messages":[{"@type":"/cosmos.staking.v1beta1.MsgCreateValidator",...}]}}'
            disabled={isSubmitting}
            rows={6}
          />
        </Box>

        <Box>
          <FieldLabel>Peer address *</FieldLabel>
          <TextInput
            value={peerAddress}
            onChange={setPeerAddress}
            placeholder="nodeId@1.2.3.4:26656"
            disabled={isSubmitting}
          />
        </Box>

        <Box>
          <FieldLabel>RPC endpoint (optional)</FieldLabel>
          <TextInput
            value={rpcEndpoint}
            onChange={setRpcEndpoint}
            placeholder="https://rpc.mynode.example.com"
            disabled={isSubmitting}
          />
        </Box>

        <Box>
          <FieldLabel>Memo (optional)</FieldLabel>
          <TextInput
            value={memo}
            onChange={setMemo}
            placeholder="My validator node"
            disabled={isSubmitting}
          />
        </Box>

        {submitError && (
          <Text fontSize="$sm" color="$textDanger">
            {submitError}
          </Text>
        )}

        <Button
          variant="primary"
          onClick={handleSubmit}
          isLoading={isSubmitting}
          disabled={isSubmitting}
        >
          {isSubmitting ? 'Signing & Submitting…' : 'Submit Join Request'}
        </Button>
      </Box>
    </PanelCard>
  );
}

// ── Peer List ─────────────────────────────────────────────────────────────────

interface PeerListSectionProps {
  launchId: string;
  authFetch: (url: string, init?: RequestInit) => Promise<Response>;
}

function PeerListSection({ launchId, authFetch }: PeerListSectionProps) {
  const [peers, setPeers] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const r = await authFetch(`/launch/${launchId}/peers?format=text`);
      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        setError((body as { message?: string }).message ?? `Server returned ${r.status}`);
        return;
      }
      const text = await r.text();
      setPeers(text);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Network error');
    } finally {
      setIsLoading(false);
    }
  }, [launchId, authFetch]);

  const handleCopy = () => {
    if (!peers) return;
    navigator.clipboard.writeText(peers).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <PanelCard title="Peer List">
      <Text fontSize="$xs" color="$textSecondary">
        Persistent peers for all approved validators. Paste into your node&apos;s <Text as="span" fontFamily="monospace" fontSize="$xs">persistent_peers</Text> config.
      </Text>
      {!peers && !isLoading && (
        <Button variant="outline" size="sm" onClick={load}>
          Load Peers
        </Button>
      )}
      {isLoading && <Text fontSize="$xs" color="$textSecondary">Loading…</Text>}
      {error && <Text fontSize="$xs" color="$textDanger">{error}</Text>}
      {peers !== null && (
        <Box display="flex" flexDirection="column" gap="8px">
          {peers === '' ? (
            <Text fontSize="$xs" color="$textSecondary">No approved peers yet.</Text>
          ) : (
            <>
              <Box
                borderRadius="6px"
                border="1px solid"
                borderColor="$divider"
                p="10px"
                attributes={{ style: { wordBreak: 'break-all' } }}
              >
                <Text fontSize="$xs" fontFamily="monospace">{peers}</Text>
              </Box>
              <Box display="flex" gap="8px">
                <Button variant="outline" size="sm" onClick={handleCopy}>
                  {copied ? 'Copied!' : 'Copy'}
                </Button>
                <Button variant="text" size="sm" onClick={load} isLoading={isLoading}>
                  Refresh
                </Button>
              </Box>
            </>
          )}
        </Box>
      )}
    </PanelCard>
  );
}

// ── H.5 — Genesis Download + SHA-256 Verification ────────────────────────────

interface GenesisSectionProps {
  launchId: string;
  finalGenesisSha256: string;
  authFetch: (url: string, init?: RequestInit) => Promise<Response>;
}

function GenesisSection({ launchId, finalGenesisSha256, authFetch }: GenesisSectionProps) {
  const [isDownloading, setIsDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [verifiedHash, setVerifiedHash] = useState<string | null>(null);
  const [hashMatch, setHashMatch] = useState<boolean | null>(null);

  const handleDownload = useCallback(async () => {
    setIsDownloading(true);
    setDownloadError(null);
    setVerifiedHash(null);
    setHashMatch(null);

    try {
      const r = await authFetch(`/launch/${launchId}/genesis`);
      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        setDownloadError((body as { message?: string }).message ?? `Server returned ${r.status}`);
        return;
      }

      const arrayBuf = await r.arrayBuffer();
      const blob = new Blob([arrayBuf], { type: 'application/json' });

      // SHA-256 verification
      const hex = await computeSha256Hex(arrayBuf);
      setVerifiedHash(hex);
      setHashMatch(hex === finalGenesisSha256);

      // Trigger browser download
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'genesis.json';
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setDownloadError(err instanceof Error ? err.message : 'Download failed');
    } finally {
      setIsDownloading(false);
    }
  }, [launchId, finalGenesisSha256, authFetch]);

  return (
    <PanelCard title="Genesis File">
      <Box display="flex" flexDirection="column" gap="8px">
        <Box display="flex" gap="8px" alignItems="baseline">
          <Text fontSize="$xs" color="$textSecondary" attributes={{ minWidth: '140px', flexShrink: '0' }}>
            Expected SHA-256
          </Text>
          <Text fontSize="$xs" fontFamily="monospace">{finalGenesisSha256}</Text>
        </Box>

        {verifiedHash && (
          <Box display="flex" gap="8px" alignItems="baseline">
            <Text fontSize="$xs" color="$textSecondary" attributes={{ minWidth: '140px', flexShrink: '0' }}>
              Downloaded SHA-256
            </Text>
            <Text
              fontSize="$xs"
              fontFamily="monospace"
              color={hashMatch ? '$textSuccess' : '$textDanger'}
            >
              {verifiedHash}
              {hashMatch ? ' ✓ match' : ' ✗ mismatch — do not use this file'}
            </Text>
          </Box>
        )}

        {downloadError && (
          <Text fontSize="$sm" color="$textDanger">{downloadError}</Text>
        )}

        <Button
          variant="primary"
          onClick={handleDownload}
          isLoading={isDownloading}
          disabled={isDownloading}
        >
          {isDownloading ? 'Downloading…' : 'Download genesis.json'}
        </Button>
      </Box>
    </PanelCard>
  );
}

// ── H.6 — Readiness Confirmation ─────────────────────────────────────────────

interface ReadinessSectionProps {
  launchId: string;
  hint: ChainHint;
  address: string;
  wallet: StatefulWallet;
  signingChainId: string;
  finalGenesisSha256: string;
  authFetch: (url: string, init?: RequestInit) => Promise<Response>;
}

function ReadinessSection({
  launchId,
  hint,
  address,
  wallet,
  signingChainId,
  finalGenesisSha256,
  authFetch,
}: ReadinessSectionProps) {
  const [binaryHash, setBinaryHash] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmed, setConfirmed] = useState(false);

  if (confirmed) {
    return (
      <PanelCard title="Readiness Confirmation">
        <Text fontSize="$sm" color="$textSuccess">
          Readiness confirmed — the coordinator will see your confirmation in the dashboard.
        </Text>
      </PanelCard>
    );
  }

  const handleConfirm = async () => {
    setError(null);

    if (!binaryHash.trim()) {
      setError('Binary SHA-256 hash is required.');
      return;
    }

    setIsSubmitting(true);
    try {
      const payload = {
        binary_hash_confirmed: binaryHash.trim(),
        genesis_hash_confirmed: finalGenesisSha256,
        operator_address: address,
      };

      const signed = await buildSignedAction(payload, wallet, signingChainId, address);

      const r = await authFetch(`/launch/${launchId}/ready`, {
        method: 'POST',
        body: JSON.stringify(signed),
      });

      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        setError((body as { message?: string }).message ?? `Server returned ${r.status}`);
        return;
      }

      setConfirmed(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unexpected error');
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <PanelCard title="Confirm Readiness">
      <Box display="flex" flexDirection="column" gap="12px">
        <Text fontSize="$sm" color="$textSecondary">
          Confirm that you have downloaded and verified the genesis file and that your node binary
          matches the expected SHA-256.
        </Text>

        <Box>
          <FieldLabel>Genesis SHA-256 (pre-filled from coordinator)</FieldLabel>
          <TextInput
            value={finalGenesisSha256}
            onChange={() => {}}
            disabled
          />
        </Box>

        <Box>
          <FieldLabel>Binary SHA-256 *</FieldLabel>
          <TextInput
            value={binaryHash}
            onChange={setBinaryHash}
            placeholder="sha256sum output of your chain binary"
            disabled={isSubmitting}
          />
        </Box>

        {error && (
          <Text fontSize="$sm" color="$textDanger">{error}</Text>
        )}

        <Button
          variant="primary"
          onClick={handleConfirm}
          isLoading={isSubmitting}
          disabled={isSubmitting}
        >
          {isSubmitting ? 'Signing & Confirming…' : 'Confirm Readiness'}
        </Button>
      </Box>
    </PanelCard>
  );
}

// ── Main export ───────────────────────────────────────────────────────────────

interface ValidatorPanelProps {
  launchId: string;
  hint: ChainHint;
  address: string;
  wallet: StatefulWallet;
  signingChainId: string;
  launch: Launch;
  dashboard: Dashboard | null;
  authFetch: (url: string, init?: RequestInit) => Promise<Response>;
}

export function ValidatorPanel({
  launchId,
  hint,
  address,
  wallet,
  signingChainId,
  launch,
  dashboard,
  authFetch,
}: ValidatorPanelProps) {
  const myReadiness = dashboard?.validators.find((v) => v.operator_address === address);
  const isApproved = !!myReadiness;
  const isReady = myReadiness?.is_ready ?? false;

  return (
    <Box display="flex" flexDirection="column" gap="16px">
      {/* H.3 / H.4 */}
      <JoinSection
        launchId={launchId}
        hint={hint}
        address={address}
        wallet={wallet}
        signingChainId={signingChainId}
        isApproved={isApproved}
        authFetch={authFetch}
      />

      {/* H.5 */}
      {isApproved && launch.final_genesis_sha256 && (
        <GenesisSection
          launchId={launchId}
          finalGenesisSha256={launch.final_genesis_sha256}
          authFetch={authFetch}
        />
      )}

      {/* H.6 */}
      {isApproved && !isReady && launch.final_genesis_sha256 && (
        <ReadinessSection
          launchId={launchId}
          hint={hint}
          address={address}
          wallet={wallet}
          signingChainId={signingChainId}
          finalGenesisSha256={launch.final_genesis_sha256}
          authFetch={authFetch}
        />
      )}

      {/* Already confirmed */}
      {isApproved && isReady && (
        <PanelCard title="Readiness">
          <Text fontSize="$sm" color="$textSuccess">
            Readiness confirmed
            {myReadiness?.last_confirmed_at
              ? ` — last confirmed at ${new Date(myReadiness.last_confirmed_at).toLocaleString()}`
              : '.'}
          </Text>
        </PanelCard>
      )}

      {/* Peer list — available once approved */}
      {isApproved && (
        <PeerListSection launchId={launchId} authFetch={authFetch} />
      )}
    </Box>
  );
}