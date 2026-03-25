## Summary

Describe the purpose of this pull request in 2-5 sentences.

## What Changed

List the main changes introduced in this PR.

- 
- 
- 

## Packages / Areas Affected

Mark the packages or areas touched by this PR.

- [ ] `cache`
- [ ] `circuitbreaker`
- [ ] `config`
- [ ] `crypto`
- [ ] `database`
- [ ] `errors`
- [ ] `health`
- [ ] `idgen`
- [ ] `logger`
- [ ] `middleware`
- [ ] `observability`
- [ ] `pagination`
- [ ] `ratelimit`
- [ ] `response`
- [ ] `router`
- [ ] `server`
- [ ] `testutil`
- [ ] `validator`
- [ ] Build / CI / tooling
- [ ] Documentation

## Why This Change

Explain the problem being solved, missing capability, bug, or cleanup this PR addresses.

## Reviewer Notes

Call out anything a reviewer should focus on.

- Critical files or functions to inspect:
- Behavior changes to verify:
- Areas where feedback is specifically wanted:

## Breaking Changes

- [ ] No breaking changes
- [ ] Breaking changes included

If breaking changes exist, describe them and note any required migration steps.

## Testing

Describe how this change was validated.

- [ ] `gofmt -w .`
- [ ] `make lint`
- [ ] `go test ./...`
- [ ] Targeted tests only
- [ ] Manual verification performed

Test details:

```text
Paste relevant commands or short notes here.
```

## Risks and Impact

Document any meaningful risks, regressions, or operational concerns.

- Risk level: Low / Medium / High
- Main risk:
- Mitigation:
- Rollback approach:

## Deployment / Rollout Notes

- [ ] No special rollout steps
- [ ] Requires config changes
- [ ] Requires environment variable updates
- [ ] Requires dependency or infrastructure changes

If any special rollout steps are needed, document them here.

## Author Checklist

- [ ] The change is scoped and focused.
- [ ] Code follows repository conventions.
- [ ] Formatting has been applied.
- [ ] Lint passes locally.
- [ ] Relevant tests were added or updated.
- [ ] Public behavior and edge cases were considered.
- [ ] Documentation was updated when needed.
- [ ] Breaking changes are clearly documented.

## Reviewer Checklist

- [ ] The PR description clearly explains the change.
- [ ] The code change matches the stated intent.
- [ ] Risks and edge cases are adequately handled.
- [ ] Tests and validation are appropriate for the change.
- [ ] Any API or behavior changes are documented.