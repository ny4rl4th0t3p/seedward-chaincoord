-- D5b: launches are private-always. The browsable PUBLIC visibility kind is removed;
-- every launch is discovery-gated (committee ∪ allowlist ∪ invited viewers). Convert any
-- existing PUBLIC rows to ALLOWLIST so no world-visible launch remains. New PUBLIC creates
-- are rejected at the service layer; IsVisibleTo no longer honours PUBLIC.
UPDATE launches SET visibility = 'ALLOWLIST' WHERE visibility = 'PUBLIC';
