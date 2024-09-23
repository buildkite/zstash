package paths

import "testing"

func TestRelPathCheck(t *testing.T) {
	type args struct {
		base string
		path string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "path within base with nested directories",
			args: args{
				base: "/code/projects/a",
				path: "/code/projects/a/vendor/bundle",
			},
			want: "vendor/bundle",
		},
		{
			name: "path within base single directory",
			args: args{
				base: "/code/projects/a",
				path: "/code/projects/a/node_modules",
			},
			want: "node_modules",
		},
		{
			name: "path outside base path",
			args: args{
				path: "/code/projects/b/node_modules",
				base: "/code/projects/a",
			},
			want: "",
		},
		{
			name: "invalid path",
			args: args{
				path: "C:\\",
				base: "/code/projects/a",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RelPathCheck(tt.args.base, tt.args.path); got != tt.want {
				t.Errorf("ResolvePath() = %v, want %v", got, tt.want)
			}
		})
	}
}
