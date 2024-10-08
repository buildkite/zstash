package commands

import (
	"errors"
	"runtime"

	"github.com/klauspost/compress/zstd"
	"github.com/mholt/archiver/v4"
)

type Globals struct {
	Debug   bool
	Version string
}

func archiveFormat(format string) (archiver.CompressedArchive, error) {
	switch format {
	case "zip":
		return archiver.CompressedArchive{
			Archival: archiver.Zip{
				Compression:          archiver.ZipMethodZstd,
				SelectiveCompression: true,
			},
		}, nil
	case "tar.zstd":
		return archiver.CompressedArchive{
			Compression: archiver.Zstd{
				EncoderOptions: []zstd.EOption{
					zstd.WithEncoderConcurrency(runtime.NumCPU()),
				},
			},
			Archival: archiver.Tar{},
		}, nil
	default:
		return archiver.CompressedArchive{}, errors.New("invalid format")
	}
}
