-- creation_signature was captured and stored but never verified (see
-- plan-chaincoord-committee-pubkeys.md and plan-chaincoord-list-visibility-hrp.md). Its only
-- load-bearing role — harvesting member[0]'s pubkey at creation — is obsolete now that proposal
-- signing sources the signer's pubkey from the ADR-036 StdSignature envelope (validated against the
-- signer's address). Drop the column.
ALTER TABLE committees DROP COLUMN creation_signature;
