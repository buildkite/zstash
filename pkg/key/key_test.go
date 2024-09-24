package key

import (
	"fmt"
	"runtime"
	"testing"
)

func TestResolve(t *testing.T) {
	type args struct {
		key   string
		paths []string
	}
	tests := []struct {
		name    string
		args    args
		env     map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "valid key from env",
			args: args{
				key: "key-{{ env `PIPELINE` }}",
			},
			env: map[string]string{
				"PIPELINE": "graylog",
			},
			want: "key-graylog",
		},
		{
			name: "valid key from os and arch",
			args: args{
				key: "key-{{ env `PIPELINE` }}-{{ os }}-{{ arch }}",
			},
			env: map[string]string{
				"PIPELINE": "graylog",
			},
			want: fmt.Sprintf("key-graylog-%s-%s", runtime.GOOS, runtime.GOARCH),
		},
		{
			name: "valid key from paths",
			args: args{
				key: "key-{{ env `PIPELINE` }}-{{ paths }}",
				paths: []string{
					"foo",
					"bar",
				},
			},
			env: map[string]string{
				"PIPELINE": "graylog",
			},
			want: "key-graylog-8843d7f92416211de9ebb963ff4ce28125932878",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := Resolve(tt.args.key, tt.args.paths)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Resolve() = %v, want %v", got, tt.want)
			}
		})
	}
}
