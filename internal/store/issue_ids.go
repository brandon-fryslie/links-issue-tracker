package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
)

const (
	issueIDPrefix               = "lit"
	issueIDCollisionProbability = 0.25
	issueIDMinHashLength        = 3
	issueIDMaxHashLength        = 8
	issueIDNonceAttempts        = 10
	base36Alphabet              = "0123456789abcdefghijklmnopqrstuvwxyz"
)

func newIssueID(ctx context.Context, tx *sql.Tx, title string, description string, createdBy string, createdAt time.Time) (string, error) {
	baseLength, err := getAdaptiveIssueIDLength(ctx, tx, issueIDPrefix)
	if err != nil {
		baseLength = 6
	}
	if baseLength > issueIDMaxHashLength {
		baseLength = issueIDMaxHashLength
	}
	for length := baseLength; length <= issueIDMaxHashLength; length++ {
		for nonce := 0; nonce < issueIDNonceAttempts; nonce++ {
			candidate := generateHashIssueID(issueIDPrefix, title, description, createdBy, createdAt, length, nonce)
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE id = ?`, candidate).Scan(&count); err != nil {
				return "", fmt.Errorf("check issue id collision: %w", err)
			}
			if count == 0 {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("generate unique issue id: exhausted lengths %d-%d", baseLength, issueIDMaxHashLength)
}

func getAdaptiveIssueIDLength(ctx context.Context, tx *sql.Tx, prefix string) (int, error) {
	numIssues, err := countTopLevelIssues(ctx, tx, prefix)
	if err != nil {
		return 6, err
	}
	return computeAdaptiveIssueIDLength(numIssues), nil
}

func countTopLevelIssues(ctx context.Context, tx *sql.Tx, prefix string) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE id LIKE ?`, prefix+"-%").Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func computeAdaptiveIssueIDLength(numIssues int) int {
	for length := issueIDMinHashLength; length <= issueIDMaxHashLength; length++ {
		if collisionProbability(numIssues, length) <= issueIDCollisionProbability {
			return length
		}
	}
	return issueIDMaxHashLength
}

func collisionProbability(numIssues int, idLength int) float64 {
	totalPossibilities := math.Pow(36, float64(idLength))
	exponent := -float64(numIssues*numIssues) / (2.0 * totalPossibilities)
	return 1.0 - math.Exp(exponent)
}

func generateHashIssueID(prefix string, title string, description string, creator string, createdAt time.Time, length int, nonce int) string {
	content := fmt.Sprintf("%s|%s|%s|%d|%d", title, description, creator, createdAt.UnixNano(), nonce)
	hash := sha256.Sum256([]byte(content))
	shortHash := encodeBase36(hash[:hashBytesForLength(length)], length)
	return fmt.Sprintf("%s-%s", prefix, shortHash)
}

func hashBytesForLength(length int) int {
	switch length {
	case 3:
		return 2
	case 4:
		return 3
	case 5, 6:
		return 4
	case 7, 8:
		return 5
	default:
		return 3
	}
}

func encodeBase36(data []byte, length int) string {
	num := new(big.Int).SetBytes(data)
	base := big.NewInt(36)
	zero := big.NewInt(0)
	mod := new(big.Int)
	chars := make([]byte, 0, length)
	for num.Cmp(zero) > 0 {
		num.DivMod(num, base, mod)
		chars = append(chars, base36Alphabet[mod.Int64()])
	}
	var result strings.Builder
	for i := len(chars) - 1; i >= 0; i-- {
		result.WriteByte(chars[i])
	}
	value := result.String()
	if len(value) < length {
		value = strings.Repeat("0", length-len(value)) + value
	}
	if len(value) > length {
		value = value[len(value)-length:]
	}
	return value
}
