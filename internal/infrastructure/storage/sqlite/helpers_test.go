package sqlite

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrToTime(t *testing.T) {
	t.Parallel()

	loc, _ := time.LoadLocation("America/New_York")
	original := time.Date(2026, 3, 27, 14, 30, 0, 123456789, time.UTC)
	eastern := time.Date(2026, 3, 27, 10, 0, 0, 0, loc)

	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "roundtrips UTC timestamp",
			input: timeToStr(original),
			want:  original,
		},
		{
			name:  "normalises non-UTC input to UTC",
			input: timeToStr(eastern),
			want:  eastern.UTC(),
		},
		{
			name:    "returns error for invalid input",
			input:   "not-a-time",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := strToTime(tc.input)
			if tc.wantErr {
				require.Error(t, err, "strToTime(%q)", tc.input)
				return
			}
			require.NoError(t, err, "strToTime(%q)", tc.input)
			assert.Equal(t, time.UTC, got.Location(), "expected UTC location")
			assert.True(t, got.Equal(tc.want), "got %v, want %v", got, tc.want)
		})
	}
}

func TestNullTimeToStr(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name    string
		input   *time.Time
		wantNil bool
	}{
		{
			name:    "nil input returns nil",
			input:   nil,
			wantNil: true,
		},
		{
			name:  "non-nil input roundtrips via strToTime",
			input: &now,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := nullTimeToStr(tc.input)
			if tc.wantNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result, "expected non-nil result")
			decoded, err := strToTime(*result)
			require.NoError(t, err, "strToTime")
			assert.True(t, decoded.Equal(tc.input.Truncate(time.Nanosecond)), "roundtrip mismatch: got %v, want %v", decoded, *tc.input)
		})
	}
}

func TestNullStrToTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   *string
		wantNil bool
		wantErr bool
	}{
		{
			name:    "nil input returns nil with no error",
			input:   nil,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := nullStrToTime(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.wantNil {
				assert.Nil(t, result, "expected nil result")
			}
		})
	}
}

func TestStrToUUID(t *testing.T) {
	t.Parallel()

	original := uuid.New()

	tests := []struct {
		name    string
		input   string
		want    uuid.UUID
		wantErr bool
	}{
		{
			name:  "roundtrips valid UUID",
			input: uuidToStr(original),
			want:  original,
		},
		{
			name:    "returns error for invalid input",
			input:   "not-a-uuid",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := strToUUID(tc.input)
			if tc.wantErr {
				require.Error(t, err, "strToUUID(%q)", tc.input)
				return
			}
			require.NoError(t, err, "strToUUID(%q)", tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNullUUIDToStr(t *testing.T) {
	t.Parallel()

	id := uuid.New()

	tests := []struct {
		name    string
		input   *uuid.UUID
		wantNil bool
	}{
		{
			name:    "nil input returns nil",
			input:   nil,
			wantNil: true,
		},
		{
			name:  "non-nil input roundtrips via nullStrToUUID",
			input: &id,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := nullUUIDToStr(tc.input)
			if tc.wantNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result, "expected non-nil result")
			decoded, err := nullStrToUUID(result)
			require.NoError(t, err, "nullStrToUUID")
			require.NotNil(t, decoded, "roundtrip returned nil")
			assert.Equal(t, *tc.input, *decoded, "roundtrip mismatch")
		})
	}
}

func TestNullStrToUUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   *string
		wantNil bool
		wantErr bool
	}{
		{
			name:    "nil input returns nil with no error",
			input:   nil,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := nullStrToUUID(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.wantNil {
				assert.Nil(t, result, "expected nil result")
			}
		})
	}
}

func TestVersionFromFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"migrations/0001_initial_schema.sql", 1, false},
		{"migrations/0042_add_index.sql", 42, false},
		{"migrations/1000_something.sql", 1000, false},
		{"migrations/no_number.sql", 0, true},
		{"migrations/.sql", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := versionFromFilename(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
