package mount

import (
	"context"
	"fmt"
	"github.com/chrislusf/seaweedfs/weed/filer"
	"github.com/chrislusf/seaweedfs/weed/glog"
	"github.com/chrislusf/seaweedfs/weed/pb/filer_pb"
	"github.com/hanwen/go-fuse/v2/fuse"
	"os"
	"strings"
	"syscall"
	"time"
)

func (wfs *WFS) Mkdir(cancel <-chan struct{}, in *fuse.MkdirIn, name string, out *fuse.EntryOut) (code fuse.Status) {

	if s := checkName(name); s != fuse.OK {
		return s
	}

	newEntry := &filer_pb.Entry{
		Name:        name,
		IsDirectory: true,
		Attributes: &filer_pb.FuseAttributes{
			Mtime:    time.Now().Unix(),
			Crtime:   time.Now().Unix(),
			FileMode: uint32(os.ModeDir) | in.Mode&^uint32(wfs.option.Umask),
			Uid:      in.Uid,
			Gid:      in.Gid,
		},
	}

	dirFullPath := wfs.inodeToPath.GetPath(in.NodeId)

	entryFullPath := dirFullPath.Child(name)

	err := wfs.WithFilerClient(false, func(client filer_pb.SeaweedFilerClient) error {

		wfs.mapPbIdFromLocalToFiler(newEntry)
		defer wfs.mapPbIdFromFilerToLocal(newEntry)

		request := &filer_pb.CreateEntryRequest{
			Directory:  string(dirFullPath),
			Entry:      newEntry,
			Signatures: []int32{wfs.signature},
		}

		glog.V(1).Infof("mkdir: %v", request)
		if err := filer_pb.CreateEntry(client, request); err != nil {
			glog.V(0).Infof("mkdir %s: %v", entryFullPath, err)
			return err
		}

		if err := wfs.metaCache.InsertEntry(context.Background(), filer.FromPbEntry(request.Directory, request.Entry)); err != nil {
			return fmt.Errorf("local mkdir dir %s: %v", entryFullPath, err)
		}

		return nil
	})

	glog.V(0).Infof("mkdir %s: %v", entryFullPath, err)

	if err != nil {
		return fuse.EIO
	}

	inode := wfs.inodeToPath.GetInode(entryFullPath)

	wfs.outputPbEntry(out, inode, newEntry)

	return fuse.OK

}

func (wfs *WFS) Rmdir(cancel <-chan struct{}, header *fuse.InHeader, name string) (code fuse.Status) {

	if name == "." {
		return fuse.Status(syscall.EINVAL)
	}
	if name == ".." {
		return fuse.Status(syscall.ENOTEMPTY)
	}

	dirFullPath := wfs.inodeToPath.GetPath(header.NodeId)
	entryFullPath := dirFullPath.Child(name)

	glog.V(3).Infof("remove directory: %v", entryFullPath)
	ignoreRecursiveErr := true // ignore recursion error since the OS should manage it
	err := filer_pb.Remove(wfs, string(dirFullPath), name, true, true, ignoreRecursiveErr, false, []int32{wfs.signature})
	if err != nil {
		glog.V(0).Infof("remove %s: %v", entryFullPath, err)
		if strings.Contains(err.Error(), filer.MsgFailDelNonEmptyFolder) {
			return fuse.Status(syscall.ENOTEMPTY)
		}
		return fuse.ENOENT
	}

	wfs.metaCache.DeleteEntry(context.Background(), entryFullPath)

	return fuse.OK

}
