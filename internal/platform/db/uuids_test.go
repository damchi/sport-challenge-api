package db

import "testing"

func TestUUIDsValue(t *testing.T) {
	tests := []struct {
		name string
		ids  UUIDs
		want string
	}{
		{name: "none", ids: nil, want: `{}`},
		{name: "one", ids: UUIDs{"7a1c0e4e-0000-4000-8000-000000000001"}, want: `{"7a1c0e4e-0000-4000-8000-000000000001"}`},
		{name: "several", ids: UUIDs{"a", "b"}, want: `{"a","b"}`},
		// Never split nor closed early: PostgreSQL rejects it as one item.
		{name: "quoted", ids: UUIDs{`a","b`, `c\`}, want: `{"a\",\"b","c\\"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.ids.Value()
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Value() = %v, want %s", got, tt.want)
			}
		})
	}
}
