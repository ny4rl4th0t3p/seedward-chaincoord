import { buildCanonicalActionPayload } from '@/utils/signedAction';

// Mock @interchain-kit/core to avoid ESM/WalletConnect load issues in Jest
jest.mock('@interchain-kit/core', () => ({ CosmosWallet: class CosmosWallet {} }));
jest.mock('@interchain-kit/react/store/stateful-wallet', () => ({
  StatefulWallet: class StatefulWallet {},
}));

describe('buildCanonicalActionPayload', () => {
  it('sorts top-level keys alphabetically', () => {
    const got = buildCanonicalActionPayload({ z: 'last', a: 'first', m: 'mid' });
    expect(got).toBe('{"a":"first","m":"mid","z":"last"}');
  });

  it('strips nonce, signature, pubkey_b64', () => {
    const got = buildCanonicalActionPayload({
      operator_address: 'cosmos1abc',
      timestamp: '2026-01-01T00:00:00Z',
      nonce: 'should-be-stripped',
      signature: 'should-be-stripped',
      pubkey_b64: 'should-be-stripped',
    });
    const parsed = JSON.parse(got);
    expect('nonce' in parsed).toBe(false);
    expect('signature' in parsed).toBe(false);
    expect('pubkey_b64' in parsed).toBe(false);
    expect(parsed.operator_address).toBe('cosmos1abc');
  });

  it('recursively sorts nested object keys', () => {
    const got = buildCanonicalActionPayload({
      gentx: { z_field: 1, a_field: 2, m_field: 3 },
      operator_address: 'cosmos1abc',
    });
    // gentx inner keys must also be sorted
    expect(got).toBe('{"gentx":{"a_field":2,"m_field":3,"z_field":1},"operator_address":"cosmos1abc"}');
  });

  it('recursively sorts deeply nested objects', () => {
    const got = buildCanonicalActionPayload({
      payload: { outer_z: { inner_z: 1, inner_a: 2 }, outer_a: 'val' },
    });
    expect(got).toBe('{"payload":{"outer_a":"val","outer_z":{"inner_a":2,"inner_z":1}}}');
  });

  it('preserves array element order (arrays are not sorted)', () => {
    const got = buildCanonicalActionPayload({ items: [3, 1, 2] });
    expect(got).toBe('{"items":[3,1,2]}');
  });

  it('handles array of objects — sorts keys within each element', () => {
    const got = buildCanonicalActionPayload({
      validators: [{ z: 1, a: 2 }, { z: 3, a: 4 }],
    });
    expect(got).toBe('{"validators":[{"a":2,"z":1},{"a":4,"z":3}]}');
  });

  it('produces no whitespace', () => {
    const got = buildCanonicalActionPayload({ a: 'val', b: 'other' });
    expect(got).not.toMatch(/\s/);
  });

  it('handles null values', () => {
    const got = buildCanonicalActionPayload({ a: null });
    expect(got).toBe('{"a":null}');
  });

  it('handles boolean and number values', () => {
    const got = buildCanonicalActionPayload({ flag: true, count: 42 });
    expect(got).toBe('{"count":42,"flag":true}');
  });

  // Canonical join-request signing payload (flat fields + nested gentx)
  it('matches expected join-request canonical form', () => {
    const got = buildCanonicalActionPayload({
      chain_id: 'mychain-1',
      gentx: { body: { messages: [{ '@type': '/cosmos.staking.v1beta1.MsgCreateValidator' }] } },
      memo: '',
      nonce: 'strip-me',
      operator_address: 'cosmos1abc',
      peer_address: '1.2.3.4:26656',
      pubkey_b64: 'strip-me',
      rpc_endpoint: '',
      signature: 'strip-me',
      timestamp: '2026-01-01T00:00:00Z',
    });

    const parsed = JSON.parse(got);
    const keys = Object.keys(parsed);
    // Must be alphabetical, must not include stripped fields
    expect(keys).toEqual([...keys].sort());
    expect(keys).not.toContain('nonce');
    expect(keys).not.toContain('signature');
    expect(keys).not.toContain('pubkey_b64');
    // gentx must be canonicalized recursively
    expect(parsed.gentx.body['@type']).toBeUndefined(); // not a key on body
    expect(Object.keys(parsed.gentx.body)).toEqual(['messages']);
  });
});