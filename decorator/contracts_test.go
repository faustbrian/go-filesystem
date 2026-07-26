package decorator_test

import (
	"github.com/faustbrian/golib/pkg/filesystem/decorator"
	filesystemFTP "github.com/faustbrian/golib/pkg/filesystem/ftp"
	filesystemLocal "github.com/faustbrian/golib/pkg/filesystem/local"
	filesystemMemory "github.com/faustbrian/golib/pkg/filesystem/memory"
	filesystemR2 "github.com/faustbrian/golib/pkg/filesystem/r2"
	filesystemS3 "github.com/faustbrian/golib/pkg/filesystem/s3"
	filesystemSFTP "github.com/faustbrian/golib/pkg/filesystem/sftp"
)

var (
	_ decorator.Backend = (*filesystemLocal.Adapter)(nil)
	_ decorator.Backend = (*filesystemMemory.Adapter)(nil)
	_ decorator.Backend = (*filesystemS3.Adapter)(nil)
	_ decorator.Backend = (*filesystemR2.Adapter)(nil)
	_ decorator.Backend = (*filesystemSFTP.Adapter)(nil)
	_ decorator.Backend = (*filesystemFTP.Adapter)(nil)
)
