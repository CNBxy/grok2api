package account

import (
	"strings"
	"testing"
)

func TestTruncateOperationRawResponseKeepsSmallValues(t *testing.T) {
	value := "short-response"
	if got := TruncateOperationRawResponse(value); got != value {
		t.Fatalf("TruncateOperationRawResponse(%q) = %q", value, got)
	}
}

func TestTruncateOperationRawResponseCapsAtLimit(t *testing.T) {
	value := strings.Repeat("a", MaxOperationRawResponseBytes+128)
	got := TruncateOperationRawResponse(value)
	if len(got) != MaxOperationRawResponseBytes {
		t.Fatalf("len = %d, want %d", len(got), MaxOperationRawResponseBytes)
	}
	if got != value[:MaxOperationRawResponseBytes] {
		t.Fatal("truncated value must be a prefix of the input")
	}
}

func TestOperationTypeAndTriggerValidation(t *testing.T) {
	if !OperationQuotaSync.IsValid() || !OperationCredentialRefresh.IsValid() {
		t.Fatal("known operation types must be valid")
	}
	if OperationType("other").IsValid() {
		t.Fatal("unknown operation type must be invalid")
	}
	if !OperationTriggerManual.IsValid() || !OperationTriggerBatch.IsValid() || !OperationTriggerScheduler.IsValid() {
		t.Fatal("known triggers must be valid")
	}
	if OperationTrigger("cron").IsValid() {
		t.Fatal("unknown trigger must be invalid")
	}
}
