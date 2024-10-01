package store

import "testing"

func Test_parseSearchResult(t *testing.T) {
	type args struct {
		stdout string
	}
	tests := []struct {
		name          string
		args          args
		wantShasum256 string
		wantExists    bool
		wantErr       bool
	}{
		{
			name: "empty",
			args: args{
				stdout: "",
			},
			wantShasum256: "",
			wantExists:    false,
			wantErr:       false,
		},
		{
			name: "exists with shasum",
			args: args{
				stdout: "key||value||value||shasum",
			},
			wantShasum256: "shasum",
			wantExists:    true,
			wantErr:       false,
		},
		{
			name: "exists with shasum two results returns first",
			args: args{
				stdout: "first||value||value||shasum1;;second||value||value||shasum2",
			},
			wantShasum256: "shasum1",
			wantExists:    true,
			wantErr:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sha256sum, exists, err := parseSearchResult(tt.args.stdout)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSearchResult() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if sha256sum != tt.wantShasum256 {
				t.Errorf("parseSearchResult() sha256sum = %v, want %v", sha256sum, tt.wantShasum256)
			}
			if exists != tt.wantExists {
				t.Errorf("parseSearchResult() exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}
