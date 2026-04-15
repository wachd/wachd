#!/bin/bash
# Copyright 2025 NTC Dev
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e

echo "════════════════════════════════════════════════════════"
echo "Wachd — Code Review & Security Check"
echo "════════════════════════════════════════════════════════"
echo

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Track failures
FAILURES=0

echo "STAGE 1: Code Quality Checks"
echo "────────────────────────────────────────────────────────"
echo

# Check 1: Run tests
echo "1. Running Go tests..."
if go test ./... 2>&1 | tee /tmp/wachd-test.log; then
    echo -e "${GREEN}PASS: All tests passed${NC}"
else
    echo -e "${RED}FAIL: Tests failed${NC}"
    FAILURES=$((FAILURES + 1))
fi
echo

# Check 2: Go vet (static analysis)
echo "2. Running go vet..."
if go vet ./...; then
    echo -e "${GREEN}PASS: go vet passed${NC}"
else
    echo -e "${RED}FAIL: go vet found issues${NC}"
    FAILURES=$((FAILURES + 1))
fi
echo

# Check 3: Build backend
echo "3. Building Go backend..."
if go build -o /tmp/wachd-server ./cmd/server && go build -o /tmp/wachd-worker ./cmd/worker; then
    echo -e "${GREEN}PASS: Backend build successful${NC}"
    rm -f /tmp/wachd-server /tmp/wachd-worker
else
    echo -e "${RED}FAIL: Backend build failed${NC}"
    FAILURES=$((FAILURES + 1))
fi
echo

# Check 4: Frontend build
echo "4. Building Next.js frontend..."
if cd web && npm run build > /dev/null 2>&1 && cd ..; then
    echo -e "${GREEN}PASS: Frontend build successful${NC}"
else
    echo -e "${RED}FAIL: Frontend build failed${NC}"
    FAILURES=$((FAILURES + 1))
fi
echo

# Check 5: Multi-tenancy isolation
echo "5. Checking multi-tenancy isolation (team_id in queries)..."
MISSING_TEAM_ID=$(grep -r "SELECT\|UPDATE\|DELETE" internal/store/*.go | grep -v "team_id" | grep -v "teams(" | wc -l | tr -d ' ')
if [ "$MISSING_TEAM_ID" -eq "0" ]; then
    echo -e "${GREEN}PASS: All queries include team_id filtering${NC}"
else
    echo -e "${YELLOW}WARN: Found $MISSING_TEAM_ID queries without team_id — manual review required${NC}"
fi
echo

echo "════════════════════════════════════════════════════════"
echo "STAGE 2: Security Review (Manual Checklist)"
echo "════════════════════════════════════════════════════════"
echo

echo "Please verify the following security checks:"
echo
echo "  SQL Injection Prevention:"
echo "    [ ] All database queries use parameterized queries (\$1, \$2, \$3)"
echo "    [ ] No string concatenation in SQL queries"
echo
echo "  Authorization & Authentication:"
echo "    [ ] Webhook endpoints validate secret before processing"
echo "    [ ] All incident/team endpoints check team_id authorization"
echo "    [ ] UUID validation prevents malicious input"
echo
echo "  PII & Data Privacy:"
echo "    [ ] Sanitizer runs BEFORE any analysis backend call"
echo "    [ ] No PII in logs (emails, IPs, secrets redacted)"
echo "    [ ] No sensitive data in API error messages"
echo
echo "  Input Validation:"
echo "    [ ] All user inputs validated (team IDs, incident IDs, snooze minutes)"
echo "    [ ] Proper HTTP status codes (400 for bad input, 404 for not found)"
echo
echo "  CORS & CSRF:"
echo "    [ ] CORS configured for expected origins only"
echo "    [ ] State-changing operations use POST/PUT/DELETE (not GET)"
echo
echo "  Secrets Management:"
echo "    [ ] No API keys, passwords, or secrets in code"
echo "    [ ] All secrets loaded from environment variables"
echo "    [ ] No secrets in git history"
echo
echo "  Dependency Security:"
echo "    [ ] No known CVEs in go.mod dependencies"
echo "    [ ] No critical npm vulnerabilities in package.json"
echo

# Summary
echo "════════════════════════════════════════════════════════"
if [ $FAILURES -eq 0 ]; then
    echo -e "${GREEN}CODE QUALITY CHECKS: ALL PASSED${NC}"
    echo
    echo "Next steps:"
    echo "  1. Complete the manual security checklist above"
    echo "  2. If all security checks pass, proceed with merge"
    echo "  3. Run: git add -A && git commit -m 'your message'"
else
    echo -e "${RED}CODE QUALITY CHECKS: $FAILURES FAILED${NC}"
    echo
    echo "Fix the issues above before proceeding to security review."
    exit 1
fi
echo "════════════════════════════════════════════════════════"
