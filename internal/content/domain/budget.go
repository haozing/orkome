package domain

import "orkome/internal/contract/apierr"

const (
	MaxSnapshotBytes  = 1 << 20
	MaxAgentInputByte = 64 << 10
	MaxTotalInputByte = 72 << 10
)

const (
	MaxAttachmentsPerHost  = 100
	MaxHostAttachmentBytes = 500 << 20
)

func ValidateSourceInput(totalBytes, agentBytes int) error {
	if agentBytes > MaxAgentInputByte {
		return apierr.NewValidation("agent input exceeds 64KiB budget")
	}
	if totalBytes > MaxTotalInputByte {
		return apierr.NewValidation("total input exceeds 72KiB budget")
	}
	return nil
}

func ValidateHostQuota(currentCount int, currentBytes, incomingBytes int64) error {
	if currentCount >= MaxAttachmentsPerHost {
		return apierr.NewValidation("host attachment quota exceeded (100 files)")
	}
	if currentBytes+incomingBytes > MaxHostAttachmentBytes {
		return apierr.NewValidation("host attachment quota exceeded (500MiB)")
	}
	return nil
}
