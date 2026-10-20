#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
BAILIAN_LIVE_READING_TEST=0 BAILIAN_LIVE_AUDIT_TEST=0 BAILIAN_LIVE_PUBLIC_COURSE_TEST=0 go test ./...
go vet ./...
node --test tests/transcript.test.cjs tests/recovery.test.cjs tests/analysis.test.cjs tests/delivery.test.cjs
node --check web/jobs.js
node --check web/recovery.js
node --check web/analysis.js
node --check web/events.js
