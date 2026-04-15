## Summary

<!-- Describe what this PR does and why. -->

## Type of Change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor
- [ ] Documentation
- [ ] Chore / dependency update

## Testing

<!-- Describe how you tested this change. -->

- [ ] Unit tests pass (`make test`)
- [ ] Build passes (`go build ./...`)
- [ ] Static analysis passes (`go vet ./...`)
- [ ] Tested manually against a running instance

## Code Review Checklist

- [ ] All database queries that access team data use `WHERE team_id = $N`
- [ ] No hardcoded secrets, API keys, or credentials
- [ ] Error handling returns errors — no panics in production paths
- [ ] New API endpoints validate input (UUIDs, required fields)
- [ ] PII sanitizer is called before any analysis backend call
- [ ] Logs include context (incident ID, team ID) where relevant

## Related Issues

<!-- Closes #<issue number> -->
