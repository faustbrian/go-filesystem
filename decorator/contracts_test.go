package decorator_test

import (
	"github.com/faustbrian/go-filesystem/v2/decorator"
	filesystemFTP "github.com/faustbrian/go-filesystem/v2/ftp"
	filesystemLocal "github.com/faustbrian/go-filesystem/v2/local"
	filesystemMemory "github.com/faustbrian/go-filesystem/v2/memory"
	filesystemR2 "github.com/faustbrian/go-filesystem/v2/r2"
	filesystemS3 "github.com/faustbrian/go-filesystem/v2/s3"
	filesystemSFTP "github.com/faustbrian/go-filesystem/v2/sftp"
)

var (
	_ decorator.Backend = (*filesystemLocal.Adapter)(nil)
	_ decorator.Backend = (*filesystemMemory.Adapter)(nil)
	_ decorator.Backend = (*filesystemS3.Adapter)(nil)
	_ decorator.Backend = (*filesystemR2.Adapter)(nil)
	_ decorator.Backend = (*filesystemSFTP.Adapter)(nil)
	_ decorator.Backend = (*filesystemFTP.Adapter)(nil)
)
