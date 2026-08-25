package decorator_test

import (
	"github.com/faustbrian/go-filesystem/decorator"
	filesystemFTP "github.com/faustbrian/go-filesystem/ftp"
	filesystemLocal "github.com/faustbrian/go-filesystem/local"
	filesystemMemory "github.com/faustbrian/go-filesystem/memory"
	filesystemR2 "github.com/faustbrian/go-filesystem/r2"
	filesystemS3 "github.com/faustbrian/go-filesystem/s3"
	filesystemSFTP "github.com/faustbrian/go-filesystem/sftp"
)

var (
	_ decorator.Backend = (*filesystemLocal.Adapter)(nil)
	_ decorator.Backend = (*filesystemMemory.Adapter)(nil)
	_ decorator.Backend = (*filesystemS3.Adapter)(nil)
	_ decorator.Backend = (*filesystemR2.Adapter)(nil)
	_ decorator.Backend = (*filesystemSFTP.Adapter)(nil)
	_ decorator.Backend = (*filesystemFTP.Adapter)(nil)
)
