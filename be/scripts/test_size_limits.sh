#!/bin/bash

# Manual Test Script for Issue #7: Request Body Size Limits
# This script tests the request size limit implementation

set -e

SERVER_URL="http://localhost:8090"
RESULTS_FILE="/tmp/size_limit_test_results.txt"

echo "========================================" | tee $RESULTS_FILE
echo "Request Size Limit Testing" | tee -a $RESULTS_FILE
echo "Date: $(date)" | tee -a $RESULTS_FILE
echo "========================================" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counter
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Helper function to run test
run_test() {
    local test_name=$1
    local expected_status=$2
    local command=$3

    TESTS_RUN=$((TESTS_RUN + 1))
    echo "----------------------------------------" | tee -a $RESULTS_FILE
    echo "Test $TESTS_RUN: $test_name" | tee -a $RESULTS_FILE
    echo "Expected: HTTP $expected_status" | tee -a $RESULTS_FILE
    echo "" | tee -a $RESULTS_FILE

    # Run command and capture output
    output=$(eval "$command" 2>&1)
    actual_status=$(echo "$output" | grep "HTTP Status:" | awk '{print $3}')

    echo "Actual: HTTP $actual_status" | tee -a $RESULTS_FILE

    if [ "$actual_status" = "$expected_status" ]; then
        echo -e "${GREEN}✓ PASS${NC}" | tee -a $RESULTS_FILE
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}✗ FAIL${NC}" | tee -a $RESULTS_FILE
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi

    echo "Response:" | tee -a $RESULTS_FILE
    echo "$output" | tee -a $RESULTS_FILE
    echo "" | tee -a $RESULTS_FILE
}

# Check if server is running
echo "Checking if server is running..." | tee -a $RESULTS_FILE
if ! curl -s -f "$SERVER_URL/api/health" > /dev/null 2>&1; then
    echo -e "${RED}ERROR: Server is not running at $SERVER_URL${NC}" | tee -a $RESULTS_FILE
    echo "Please start the server with: go run ." | tee -a $RESULTS_FILE
    exit 1
fi
echo -e "${GREEN}Server is running${NC}" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE

# Test 1: Normal-sized request (should succeed with 200 or 4xx depending on auth)
echo "=== Test 1: Normal Request ===" | tee -a $RESULTS_FILE
run_test "Normal scan request (small payload)" \
    "200" \
    'curl -X POST $SERVER_URL/api/scans \
      -H "Content-Type: application/json" \
      -d '"'"'{"ScanType":"Local","LocalScan":{"Source":"/tmp/test"}}'"'"' \
      -w "\nHTTP Status: %{http_code}\n" \
      -s'

# Test 2: Create test files for size testing
echo "Creating test files..." | tee -a $RESULTS_FILE

# Create 500 KB file (within 512 KB default limit)
echo "Creating 500 KB test file..." | tee -a $RESULTS_FILE
python3 -c "print('a' * 512000)" > /tmp/500kb.txt

# Create exactly 1 MB JSON (at scan endpoint limit)
echo "Creating 1 MB JSON test file..." | tee -a $RESULTS_FILE
python3 -c "import json; print(json.dumps({'ScanType':'Local','LocalScan':{'Source':'a'*1048000}}))" > /tmp/1mb.json

# Create 2 MB file (exceeds scan limit)
echo "Creating 2 MB test file..." | tee -a $RESULTS_FILE
dd if=/dev/zero of=/tmp/2mb.bin bs=1M count=2 2>/dev/null

echo -e "${GREEN}Test files created${NC}" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE

# Test 3: Request at the limit (1 MB - should succeed)
echo "=== Test 2: Request at Limit (1 MB) ===" | tee -a $RESULTS_FILE
run_test "Scan request at 1 MB limit (should be processed)" \
    "200" \
    'curl -X POST $SERVER_URL/api/scans \
      -H "Content-Type: application/json" \
      -d @/tmp/1mb.json \
      -w "\nHTTP Status: %{http_code}\n" \
      -s'

# Test 4: Oversized request (2 MB - should fail with 413)
echo "=== Test 3: Oversized Request (2 MB) ===" | tee -a $RESULTS_FILE
run_test "Oversized scan request (should return 413)" \
    "413" \
    'curl -X POST $SERVER_URL/api/scans \
      -H "Content-Type: application/json" \
      -d @/tmp/2mb.bin \
      -w "\nHTTP Status: %{http_code}\n" \
      -s'

# Test 5: Verify error response format
echo "=== Test 4: Verify Error Response Format ===" | tee -a $RESULTS_FILE
echo "Sending oversized request and checking error format..." | tee -a $RESULTS_FILE

response=$(curl -X POST $SERVER_URL/api/scans \
  -H "Content-Type: application/json" \
  -d @/tmp/2mb.bin \
  -s)

echo "Response:" | tee -a $RESULTS_FILE
echo "$response" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE

# Check if response contains expected error fields
if echo "$response" | grep -q "PAYLOAD_TOO_LARGE"; then
    echo -e "${GREEN}✓ Error code present${NC}" | tee -a $RESULTS_FILE
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗ Error code missing${NC}" | tee -a $RESULTS_FILE
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if echo "$response" | grep -q "max_size_bytes"; then
    echo -e "${GREEN}✓ Max size details present${NC}" | tee -a $RESULTS_FILE
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗ Max size details missing${NC}" | tee -a $RESULTS_FILE
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

TESTS_RUN=$((TESTS_RUN + 2))
echo "" | tee -a $RESULTS_FILE

# Test 6: Multiple oversized requests (stress test)
echo "=== Test 5: Multiple Oversized Requests ===" | tee -a $RESULTS_FILE
echo "Sending 10 concurrent oversized requests..." | tee -a $RESULTS_FILE

start_time=$(date +%s)
for i in {1..10}; do
    curl -X POST $SERVER_URL/api/scans \
      -H "Content-Type: application/json" \
      -d @/tmp/2mb.bin \
      -w "\n" \
      -s > /dev/null &
done
wait
end_time=$(date +%s)
duration=$((end_time - start_time))

echo "Completed in ${duration} seconds" | tee -a $RESULTS_FILE

# Check if server is still responsive
if curl -s -f "$SERVER_URL/api/health" > /dev/null 2>&1; then
    echo -e "${GREEN}✓ Server remained responsive${NC}" | tee -a $RESULTS_FILE
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗ Server became unresponsive${NC}" | tee -a $RESULTS_FILE
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
TESTS_RUN=$((TESTS_RUN + 1))
echo "" | tee -a $RESULTS_FILE

# Test 7: Check server logs for size limit warnings
echo "=== Test 6: Verify Logging ===" | tee -a $RESULTS_FILE
echo "Checking if oversized requests are being logged..." | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE
echo "Instructions:" | tee -a $RESULTS_FILE
echo "1. Check server console output" | tee -a $RESULTS_FILE
echo "2. Look for WARNING logs with message: 'Request body size limit exceeded'" | tee -a $RESULTS_FILE
echo "3. Verify logs include: remote_addr, method, path, max_bytes, max_human" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE
echo "Example expected log:" | tee -a $RESULTS_FILE
echo "WARN Request body size limit exceeded remote_addr=127.0.0.1:XXXXX method=POST path=/api/scans max_bytes=1048576 max_human='1.0 MB'" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE

# Summary
echo "========================================" | tee -a $RESULTS_FILE
echo "Test Summary" | tee -a $RESULTS_FILE
echo "========================================" | tee -a $RESULTS_FILE
echo "Total Tests Run: $TESTS_RUN" | tee -a $RESULTS_FILE
echo -e "${GREEN}Tests Passed: $TESTS_PASSED${NC}" | tee -a $RESULTS_FILE
echo -e "${RED}Tests Failed: $TESTS_FAILED${NC}" | tee -a $RESULTS_FILE
echo "" | tee -a $RESULTS_FILE

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}ALL TESTS PASSED ✓${NC}" | tee -a $RESULTS_FILE
    echo "" | tee -a $RESULTS_FILE
    echo "Next Steps:" | tee -a $RESULTS_FILE
    echo "1. Review server logs to confirm oversized requests are logged" | tee -a $RESULTS_FILE
    echo "2. Check that error responses have proper JSON format" | tee -a $RESULTS_FILE
    echo "3. Proceed with deployment" | tee -a $RESULTS_FILE
    exit 0
else
    echo -e "${RED}SOME TESTS FAILED ✗${NC}" | tee -a $RESULTS_FILE
    echo "" | tee -a $RESULTS_FILE
    echo "Review failures above and fix before deploying." | tee -a $RESULTS_FILE
    exit 1
fi

# Cleanup
rm -f /tmp/500kb.txt /tmp/1mb.json /tmp/2mb.bin
