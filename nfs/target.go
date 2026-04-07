// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package nfs

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lyp256/go-nfs-client/nfs/rpc"
	"github.com/lyp256/go-nfs-client/nfs/util"
	"github.com/lyp256/go-nfs-client/nfs/xdr"
)

type cachedDir struct {
	// fh      []byte
	entries map[string]*EntryPlus
	expire  time.Time
}

type Target struct {
	*rpc.Client

	auth    rpc.Auth
	fh      []byte
	dirPath string
	fsinfo  *FSInfo

	entryTimeout time.Duration
	cacheM       sync.Mutex
	cachedTree   map[string]*cachedDir
}

func NewTarget(addr string, auth rpc.Auth, fh []byte, dirpath string, entryTimeout time.Duration) (*Target, error) {
	m := rpc.Mapping{
		Prog: Nfs3Prog,
		Vers: Nfs3Vers,
		Prot: rpc.IPProtoTCP,
		Port: 0,
	}

	client, err := DialService(addr, m)
	if err != nil {
		return nil, err
	}

	return NewTargetWithClient(client, auth, fh, dirpath, entryTimeout)
}

func NewTargetWithClient(client *rpc.Client, auth rpc.Auth, fh []byte, dirpath string, entryTimeout time.Duration) (*Target, error) {
	vol := &Target{
		Client:       client,
		auth:         auth,
		fh:           fh,
		dirPath:      dirpath,
		entryTimeout: entryTimeout,
		cachedTree:   make(map[string]*cachedDir),
	}

	fsinfo, err := vol.FSInfo()
	if err != nil {
		return nil, err
	}

	vol.fsinfo = fsinfo
	util.Debugf("%s fsinfo=%#v", dirpath, fsinfo)
	go vol.cleanupCache()
	return vol, nil
}

// wraps the Call function to check status and decode errors
func (v *Target) call(c interface{}) (io.ReadSeeker, error) {
	res, err := v.Call(c)
	if err != nil {
		return nil, err
	}

	status, err := xdr.ReadUint32(res)
	if err != nil {
		return nil, err
	}

	if err = NFS3Error(status); err != nil {
		return nil, err
	}

	return res, nil
}

func (v *Target) FSInfo() (*FSInfo, error) {
	type FSInfoArgs struct {
		rpc.Header
		FsRoot []byte
	}

	res, err := v.call(&FSInfoArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3FSInfo,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		FsRoot: v.fh,
	})

	if err != nil {
		util.Debugf("fsroot: %s", err.Error())
		return nil, err
	}

	fsinfo := new(FSInfo)
	if err = xdr.Read(res, fsinfo); err != nil {
		return nil, err
	}

	return fsinfo, nil
}

// FSStat returns filesystem statistics including total, used, and available space.
func (v *Target) FSStat() (*FSStat, error) {
	type FSStatArgs struct {
		rpc.Header
		FsRoot []byte
	}

	res, err := v.call(&FSStatArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3FSStat,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		FsRoot: v.fh,
	})

	if err != nil {
		util.Debugf("fsstat: %s", err.Error())
		return nil, err
	}

	fsstat := new(FSStat)
	if err = xdr.Read(res, fsstat); err != nil {
		return nil, err
	}

	return fsstat, nil
}

func (v *Target) cleanupCache() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-v.Client.Context().Done():
			return
		case <-ticker.C:
		}
		v.cacheM.Lock()
		now := time.Now()
		var cnt int
		for ino, es := range v.cachedTree {
			if now.After(es.expire) {
				delete(v.cachedTree, ino)
			}
			cnt++
			if cnt > 1000 {
				break
			}
		}
		v.cacheM.Unlock()
	}
}

// Lookup returns attributes and the file handle to a given dirent
func (v *Target) Lookup(p string, cached ...bool) (os.FileInfo, []byte, error) {
	var (
		err   error
		fattr *Fattr
		fh    = v.fh
	)

	// desecend down a path heirarchy to get the last elem's fh
	dirents := strings.Split(path.Clean(p), "/")
	for _, dirent := range dirents {
		// we're assuming the root is always the root of the mount
		if dirent == "." || dirent == "" {
			continue
		}

		if len(cached) > 0 && cached[0] {
			fattr, fh, err = v.cachedLookup(fh, dirent)
		} else {
			fattr, fh, err = v.lookup(fh, dirent)
		}
		if err != nil {
			return nil, nil, err
		}

		//util.Debugf("%s -> 0x%x", dirent, fh)
		// TODO: resolve symlink
	}

	if fattr == nil {
		fattr, err = v.GetAttr(v.fh)
		if err != nil {
			return nil, nil, err
		}
	}

	return fattr, fh, nil
}

// lookup2 returns attributes and the file handle for a given path.
// Unlike Lookup, it does not support caching and skips the root directory entry.
func (v *Target) lookup2(p string) (*Fattr, []byte, error) {
	var (
		err   error
		fattr *Fattr
		fh    = v.fh
	)

	// desecend down a path heirarchy to get the last elem's fh
	dirents := strings.Split(path.Clean(p), "/")
	for _, dirent := range dirents {
		// we're assuming the root is always the root of the mount
		if dirent == "." || dirent == "" {
			util.Debugf("root -> 0x%x", fh)
			continue
		}

		fattr, fh, err = v.lookup(fh, dirent)
		if err != nil {
			return nil, nil, err
		}

		//util.Debugf("%s -> 0x%x", dirent, fh)
	}

	if fattr == nil {
		fattr, err = v.GetAttr(v.fh)
		if err != nil {
			return nil, nil, err
		}
	}

	return fattr, fh, nil
}

func (v *Target) parsefh(fh []byte) string {
	return string(fh)
}

func (v *Target) cachedLookup(fh []byte, name string) (*Fattr, []byte, error) {
	ino := v.parsefh(fh)

	// First check cache while holding lock
	v.cacheM.Lock()
	if es, ok := v.cachedTree[ino]; ok && time.Since(es.expire) < 0 {
		if e, ok := es.entries[name]; ok {
			v.cacheM.Unlock()
			return &e.Attr.Attr, e.Handle.FH, nil
		}
		v.cacheM.Unlock()
		return nil, nil, os.ErrNotExist
	}
	v.cacheM.Unlock()

	// Cache miss or expired - refresh without lock
	if err := v.refreshDirCache(fh); err != nil {
		return nil, nil, err
	}

	// Check again after refresh
	v.cacheM.Lock()
	defer v.cacheM.Unlock()
	if es, ok := v.cachedTree[ino]; ok {
		if e, ok := es.entries[name]; ok {
			return &e.Attr.Attr, e.Handle.FH, nil
		}
	}
	return nil, nil, os.ErrNotExist
}

func (v *Target) invalidateEntryCache(fh []byte) {
	ino := v.parsefh(fh)
	v.cacheM.Lock()
	// FIXME: refine
	delete(v.cachedTree, ino)
	v.cacheM.Unlock()
}

// lookup returns the same as above, but by fh and name
func (v *Target) lookup(fh []byte, name string) (*Fattr, []byte, error) {
	type Lookup3Args struct {
		rpc.Header
		What Diropargs3
	}

	type LookupOk struct {
		FH      []byte
		Attr    PostOpAttr
		DirAttr PostOpAttr
	}

	res, err := v.call(&Lookup3Args{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3Lookup,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		What: Diropargs3{
			FH:       fh,
			Filename: name,
		},
	})

	if err != nil {
		util.Debugf("lookup(%s): %s", name, err.Error())
		return nil, nil, err
	}

	lookupres := new(LookupOk)
	if err := xdr.Read(res, lookupres); err != nil {
		util.Errorf("lookup(%s) failed to parse return: %s", name, err)
		util.Debugf("lookup partial decode: %+v", *lookupres)
		return nil, nil, err
	}

	util.Debugf("lookup(%s): FH 0x%x, attr: %+v", name, lookupres.FH, lookupres.Attr.Attr)
	return &lookupres.Attr.Attr, lookupres.FH, nil
}

// Access file
func (v *Target) Access(path string, mode uint32) (uint32, error) {

	_, fh, err := v.Lookup(path)
	if err != nil {
		return 0, err
	}

	_, mode, err = v.access(fh, path, mode)

	return mode, err
}

// access returns the same as above, but by fh and name
func (v *Target) access(fh []byte, path string, access uint32) (*Fattr, uint32, error) {
	type Access3Args struct {
		rpc.Header
		FH     []byte
		Access uint32
	}

	type AccessOk struct {
		Attr   PostOpAttr
		Access uint32
	}

	res, err := v.call(&Access3Args{Header: rpc.Header{
		Rpcvers: 2,
		Prog:    Nfs3Prog,
		Vers:    Nfs3Vers,
		Proc:    NFSProc3Access,
		Cred:    v.auth,
		Verf:    rpc.AuthNull,
	},
		FH:     fh,
		Access: access})

	if err != nil {
		util.Debugf("access(%s): %s", path, err.Error())
		return nil, 0, err
	}

	accessres := new(AccessOk)

	if err := xdr.Read(res, accessres); err != nil {
		util.Errorf("access(%s) failed to parse return: %s", path, err)
		util.Debugf("access partial decode: %+v", *accessres)
		return nil, 0, err
	}

	util.Debugf("access(%s): access %d, attr: %+v", path, accessres.Access, accessres.Attr)

	return &accessres.Attr.Attr, accessres.Access, nil
}

// Getattr file
func (v *Target) Getattr(path string) (*Fattr, error) {

	_, fh, err := v.Lookup(path)
	if err != nil {
		return nil, err
	}

	attr, err := v.getattr(fh, path)

	return attr, err
}

func (v *Target) getattr(fh []byte, path string) (*Fattr, error) {

	type Getattr3Args struct {
		rpc.Header
		FH []byte
	}

	type GetattrOk struct {
		Attr Fattr
	}

	res, err := v.call(&Getattr3Args{Header: rpc.Header{
		Rpcvers: 2,
		Prog:    Nfs3Prog,
		Vers:    Nfs3Vers,
		Proc:    NFSProc3GetAttr,
		Cred:    v.auth,
		Verf:    rpc.AuthNull,
	},
		FH: fh})

	if err != nil {
		util.Debugf("getattr(%s): %s", path, err.Error())
		return nil, err
	}

	getattrres := new(GetattrOk)

	if err := xdr.Read(res, getattrres); err != nil {
		util.Errorf("getattr(%s) failed to parse return: %s", path, err)
		util.Debugf("getattr partial decode: %+v", *getattrres)
		return nil, err
	}

	util.Debugf("getattr(%s): attr: %+v", path, getattrres.Attr)

	return &getattrres.Attr, nil
}

// Setattr set file attr
func (v *Target) Setattr(path string, sattr Sattr3) error {

	attr, fh, err := v.lookup2(path)
	if err != nil {
		return err
	}

	err = v.setattr(fh, path, sattr, Sattrguard3{Check: 1, Time: attr.Ctime})
	return err
}

func (v *Target) setattr(fh []byte, path string, sattr Sattr3, guard Sattrguard3) error {

	type Setattr3Args struct {
		rpc.Header
		FH    []byte
		Sattr Sattr3
		Guard Sattrguard3
	}

	type SetattrOk struct {
		FileWcc WccData
	}

	res, err := v.call(&Setattr3Args{Header: rpc.Header{
		Rpcvers: 2,
		Prog:    Nfs3Prog,
		Vers:    Nfs3Vers,
		Proc:    NFSProc3SetAttr,
		Cred:    v.auth,
		Verf:    rpc.AuthNull,
	},
		FH:    fh,
		Sattr: sattr,
		Guard: guard})
	if err != nil {
		util.Debugf("setattr(%s): %s", path, err.Error())
		return err
	}

	setattrres := new(SetattrOk)

	if err := xdr.Read(res, setattrres); err != nil {
		util.Errorf("setattr(%s) failed to parse return: %s", path, err)
		util.Debugf("setattr partial decode: %+v", *setattrres)
		return err
	}
	util.Debugf("setattr(%s): FileWcc: %+v", path, setattrres.FileWcc)

	return nil
}

// ReadDirPlus get dir sub item
func (v *Target) ReadDirPlus(dir string) ([]*EntryPlus, error) {
	_, fh, err := v.Lookup(dir)
	if err != nil {
		return nil, err
	}

	ino := v.parsefh(fh)

	// Check cache while holding lock
	v.cacheM.Lock()
	if es, ok := v.cachedTree[ino]; ok && time.Since(es.expire) < 0 {
		var result []*EntryPlus
		for _, e := range es.entries {
			if e.FileName == "." || e.FileName == ".." {
				continue
			}
			result = append(result, e)
		}
		v.cacheM.Unlock()
		return result, nil
	}
	v.cacheM.Unlock()

	// Cache miss or expired - refresh without lock
	if err = v.refreshDirCache(fh); err != nil {
		return nil, err
	}

	// Return results after refresh
	v.cacheM.Lock()
	defer v.cacheM.Unlock()
	var result []*EntryPlus
	if es, ok := v.cachedTree[ino]; ok {
		for _, e := range es.entries {
			if e.FileName == "." || e.FileName == ".." {
				continue
			}
			result = append(result, e)
		}
	}
	return result, nil
}

// refreshDirCache refreshes the directory cache for the given filehandle.
// This function performs network operations and should NOT be called while holding v.cacheM.
func (v *Target) refreshDirCache(fh []byte) error {
	ino := v.parsefh(fh)

	var (
		entries    []*EntryPlus
		entriesMap map[string]*EntryPlus
		err        error
		dattr      *Fattr
	)

	for {
		entriesMap = make(map[string]*EntryPlus)
		entries, err = v.readDirPlus(fh)
		if err != nil {
			break
		}
		for _, entry := range entries {
			entriesMap[entry.FileName] = entry
		}
		dir := entriesMap["."]
		if dir == nil {
			continue
		}
		dattr, err = v.GetAttr(fh)
		if err != nil {
			break
		}
		if dattr.ModTime().Equal(dir.ModTime()) {
			break
		}
	}

	if err != nil {
		return err
	}

	// Update cache with lock
	v.cacheM.Lock()
	defer v.cacheM.Unlock()

	// Check if another goroutine already updated the cache
	es, ok := v.cachedTree[ino]
	if ok && time.Since(es.expire) < 0 {
		if !entriesMap["."].ModTime().After(es.entries["."].ModTime()) {
			return nil
		}
	}
	if !ok {
		es = &cachedDir{}
		v.cachedTree[ino] = es
	}
	es.entries = entriesMap
	es.expire = time.Now().Add(v.entryTimeout)
	return nil
}

// GetAttr returns the attributes of the file/directory specified by fh
func (v *Target) GetAttr(fh []byte) (*Fattr, error) {
	type GetAttr3Args struct {
		rpc.Header
		FH []byte
	}

	type GetAttr3Res struct {
		Attr struct {
			Attr Fattr
		}
	}

	res, err := v.call(&GetAttr3Args{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3GetAttr,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		FH: fh,
	})

	if err != nil {
		return nil, err
	}

	getattrres := new(GetAttr3Res)
	if err := xdr.Read(res, getattrres); err != nil {
		return nil, err
	}

	return &getattrres.Attr.Attr, nil
}

// SetAttr sets the attributes of the file/directory specified by fh
func (v *Target) SetAttr(fh []byte, sattr Sattr3) (*Fattr, error) {
	type GuardTime struct {
		IsSet     bool     `xdr:"union"`
		GuardTime NFS3Time `xdr:"unioncase=1"`
	}

	type SetAttr3Args struct {
		rpc.Header
		FH        []byte
		Attr      Sattr3
		GuardTime GuardTime
	}

	type SetAttr3Res struct {
		DirWcc WccData
	}

	res, err := v.call(&SetAttr3Args{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3SetAttr,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		FH:   fh,
		Attr: sattr,
	})

	if err != nil {
		return nil, err
	}

	setattrres := new(SetAttr3Res)
	if err := xdr.Read(res, setattrres); err != nil {
		return nil, err
	}
	return &setattrres.DirWcc.After.Attr, nil
}

func (v *Target) readDirPlus(fh []byte) ([]*EntryPlus, error) {
	cookie := uint64(0)
	cookieVerf := uint64(0)
	eof := false

	type ReadDirPlus3Args struct {
		rpc.Header
		FH         []byte
		Cookie     uint64
		CookieVerf uint64
		DirCount   uint32
		MaxCount   uint32
	}

	type DirListPlus3 struct {
		IsSet bool      `xdr:"union"`
		Entry EntryPlus `xdr:"unioncase=1"`
	}

	type DirListOK struct {
		DirAttrs   PostOpAttr
		CookieVerf uint64
	}

	var entries []*EntryPlus
	for !eof {
		res, err := v.call(&ReadDirPlus3Args{
			Header: rpc.Header{
				Rpcvers: 2,
				Prog:    Nfs3Prog,
				Vers:    Nfs3Vers,
				Proc:    NFSProc3ReadDirPlus,
				Cred:    v.auth,
				Verf:    rpc.AuthNull,
			},
			FH:         fh,
			Cookie:     cookie,
			CookieVerf: cookieVerf,
			DirCount:   512,
			MaxCount:   4096,
		})

		if err != nil {
			util.Debugf("readdir(%x): %s", fh, err.Error())
			return nil, err
		}

		// The dir list entries are so-called "optional-data".  We need to check
		// the Follows fields before continuing down the array.  Effectively, it's
		// an encoding used to flatten a linked list into an array where the
		// Follows field is set when the next idx has data. See
		// https://tools.ietf.org/html/rfc4506.html#section-4.19 for details.
		dirlistOK := new(DirListOK)
		if err = xdr.Read(res, dirlistOK); err != nil {
			util.Errorf("readdir failed to parse result (%x): %s", fh, err.Error())
			util.Debugf("partial dirlist: %+v", dirlistOK)
			return nil, err
		}

		for {
			var item DirListPlus3
			if err = xdr.Read(res, &item); err != nil {
				util.Errorf("readdir failed to parse directory entry, aborting")
				util.Debugf("partial dirent: %+v", item)
				return nil, err
			}

			if !item.IsSet {
				break
			}

			cookie = item.Entry.Cookie
			entries = append(entries, &item.Entry)
		}

		if err = xdr.Read(res, &eof); err != nil {
			util.Errorf("readdir failed to determine presence of more data to read, aborting")
			return nil, err
		}

		util.Debugf("No EOF for dirents so calling back for more")
		cookieVerf = dirlistOK.CookieVerf
	}

	return entries, nil
}

// Mkdir Creates a directory of the given name and returns its handle
func (v *Target) Mkdir(path string, perm os.FileMode) ([]byte, error) {
	path = filepath.Clean(path)
	dir, newDir := filepath.Split(path)
	if newDir == "" || newDir == "/" || newDir == "." {
		return nil, nil
	}

	_, fh, err := v.Lookup(dir)
	if err != nil {
		return nil, err
	}

	type MkdirArgs struct {
		rpc.Header
		Where Diropargs3
		Attrs Sattr3
	}

	type MkdirOk struct {
		FH     PostOpFH3
		Attr   PostOpAttr
		DirWcc WccData
	}

	args := &MkdirArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3Mkdir,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		Where: Diropargs3{
			FH:       fh,
			Filename: newDir,
		},
		Attrs: Sattr3{
			Mode: SetMode{
				SetIt: true,
				Mode:  uint32(perm.Perm()),
			},
		},
	}
	res, err := v.call(args)

	if err != nil {
		util.Debugf("mkdir(%s): %s", path, err.Error())
		util.Debugf("mkdir args (%+v)", args)
		return nil, err
	}

	mkdirres := new(MkdirOk)
	if err := xdr.Read(res, mkdirres); err != nil {
		util.Errorf("mkdir(%s) failed to parse return: %s", path, err)
		util.Debugf("mkdir(%s) partial response: %+v", mkdirres)
		return nil, err
	}
	v.invalidateEntryCache(fh)
	util.Debugf("mkdir(%s): created successfully (0x%x)", path, fh)
	return mkdirres.FH.FH, nil
}

// MkdirAll creates a directory and all parent directories as needed.
func (v *Target) MkdirAll(path string, perm os.FileMode) ([]byte, error) {
	path = filepath.Clean(path)

	// Check if path exists and is a directory.
	fi, fh, err := v.Lookup(path)
	if err == nil {
		if fi.IsDir() {
			return fh, nil
		}
		return nil, os.ErrExist
	}

	// If the parent directory does not exist, create it.
	dir, _ := filepath.Split(path)
	if dir != "" && dir != "/" && dir != "." {
		_, err = v.MkdirAll(dir, perm)
		if err != nil {
			return nil, err
		}
	}

	// Parent now exists; create the directory.
	fh, err = v.Mkdir(path, perm)
	if err != nil {
		if os.IsExist(err) {
			// Between the Lookup and the Mkdir, someone else created it.
			// Check if it's a directory.
			fi, fh, err := v.Lookup(path)
			if err == nil && fi.IsDir() {
				return fh, nil
			}
		}
		return nil, err
	}

	return fh, nil
}

// Create a file with name the given mode
func (v *Target) Create(path string, perm os.FileMode) ([]byte, error) {
	path = filepath.Clean(path)
	dir, newFile := filepath.Split(path)
	if newFile == "" || newFile == "/" || newFile == "." {
		return nil, os.ErrInvalid
	}

	_, fh, err := v.Lookup(dir)
	if err != nil {
		return nil, err
	}

	type How struct {
		// 0 : UNCHECKED (default)
		// 1 : GUARDED
		// 2 : EXCLUSIVE
		Mode uint32
		Attr Sattr3
	}
	type Create3Args struct {
		rpc.Header
		Where Diropargs3
		HW    How
	}

	type Create3Res struct {
		FH     PostOpFH3
		Attr   PostOpAttr
		DirWcc WccData
	}

	res, err := v.call(&Create3Args{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3Create,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		Where: Diropargs3{
			FH:       fh,
			Filename: newFile,
		},
		HW: How{
			Attr: Sattr3{
				Mode: SetMode{
					SetIt: true,
					Mode:  uint32(perm.Perm()),
				},
			},
		},
	})

	if err != nil {
		util.Debugf("create(%s): %s", path, err.Error())
		return nil, err
	}

	status := new(Create3Res)
	if err = xdr.Read(res, status); err != nil {
		return nil, err
	}
	v.invalidateEntryCache(fh)
	util.Debugf("create(%s): created successfully", path)
	return status.FH.FH, nil
}

// Remove a file
func (v *Target) Remove(path string) error {
	path = filepath.Clean(path)
	parentDir, deleteFile := filepath.Split(path)
	if deleteFile == "" || deleteFile == "/" || deleteFile == "." {
		return os.ErrInvalid
	}

	_, fh, err := v.Lookup(parentDir)
	if err != nil {
		return err
	}

	return v.remove(fh, deleteFile)
}

// remove the named file from the parent (fh)
func (v *Target) remove(fh []byte, deleteFile string) error {
	type RemoveArgs struct {
		rpc.Header
		Object Diropargs3
	}

	_, err := v.call(&RemoveArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3Remove,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		Object: Diropargs3{
			FH:       fh,
			Filename: deleteFile,
		},
	})

	if err != nil {
		util.Debugf("remove(%s): %s", deleteFile, err.Error())
		return err
	}
	v.invalidateEntryCache(fh)
	return nil
}

// RmDir removes a non-empty directory
func (v *Target) RmDir(path string) error {
	path = filepath.Clean(path)
	dir, deletedir := filepath.Split(path)
	if deletedir == "" || deletedir == "/" || deletedir == "." {
		return os.ErrInvalid
	}

	_, fh, err := v.Lookup(dir)
	if err != nil {
		return err
	}

	return v.rmDir(fh, deletedir)
}

// delete the named directory from the parent directory (fh)
func (v *Target) rmDir(fh []byte, name string) error {
	type RmDir3Args struct {
		rpc.Header
		Object Diropargs3
	}

	_, err := v.call(&RmDir3Args{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3RmDir,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		Object: Diropargs3{
			FH:       fh,
			Filename: name,
		},
	})

	if err != nil {
		util.Debugf("rmdir(%s): %s", name, err.Error())
		return err
	}
	v.invalidateEntryCache(fh)
	util.Debugf("rmdir(%s): deleted successfully", name)
	return nil
}

func (v *Target) RemoveAll(path string) error {
	parentDir, deleteDir := filepath.Split(path)
	_, parentDirfh, err := v.Lookup(parentDir)
	if err != nil {
		return err
	}

	// Easy path.  This is a directory and it's empty.  If not a dir or not an
	// empty dir, this will throw an error.
	err = v.rmDir(parentDirfh, deleteDir)
	if err == nil || os.IsNotExist(err) {
		return nil
	}

	// Collect the not a dir error.
	if IsNotDirError(err) {
		return err
	}

	_, deleteDirfh, err := v.lookup(parentDirfh, deleteDir)
	if err != nil {
		return err
	}

	if err = v.removeAll(deleteDirfh); err != nil {
		return err
	}

	// Delete the directory we started at.
	if err = v.rmDir(parentDirfh, deleteDir); err != nil {
		return err
	}

	return nil
}

// removeAll removes the deleteDir recursively
func (v *Target) removeAll(deleteDirfh []byte) error {

	// BFS the dir tree recursively.  If dir, recurse, then delete the dir and
	// all files.

	// This is a directory, get all of its Entries
	entries, err := v.readDirPlus(deleteDirfh)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		// skip "." and ".."
		if entry.FileName == "." || entry.FileName == ".." {
			continue
		}

		// If directory, recurse, then nuke it.  It should be empty when we get
		// back.
		if entry.Attr.Attr.Type == NF3Dir {
			fh := entry.Handle.FH
			if !entry.Handle.IsSet {
				_, fh, err = v.lookup(deleteDirfh, entry.FileName)
				if err != nil {
					return err
				}
			}

			if err = v.removeAll(fh); err != nil {
				return err
			}

			err = v.rmDir(deleteDirfh, entry.FileName)
		} else {

			// nuke all files
			err = v.remove(deleteDirfh, entry.FileName)
		}

		if err != nil {
			util.Errorf("error deleting %s: %s", entry.FileName, err.Error())
			return err
		}
	}

	return nil
}

// Rename a file or directory
func (v *Target) Rename(from, to string) error {
	from = filepath.Clean(from)
	to = filepath.Clean(to)

	parentSrc, src := filepath.Split(from)
	if src == "" || src == "/" || src == "." {
		return os.ErrInvalid
	}

	_, fhSrc, err := v.Lookup(parentSrc)
	if err != nil {
		return err
	}

	parentDst, dst := filepath.Split(to)
	if dst == "" || dst == "/" || dst == "." {
		return os.ErrInvalid
	}

	_, fhDst, err := v.Lookup(parentDst)
	if err != nil {
		return err
	}

	return v.rename(fhSrc, src, fhDst, dst)
}

// rename a file or directory
func (v *Target) rename(fhSrc []byte, src string, fhDst []byte, dst string) error {
	type RenameArgs struct {
		rpc.Header
		From Diropargs3
		To   Diropargs3
	}

	_, err := v.call(&RenameArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3Rename,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		From: Diropargs3{
			FH:       fhSrc,
			Filename: src,
		},
		To: Diropargs3{
			FH:       fhDst,
			Filename: dst,
		},
	})

	if err != nil {
		util.Debugf("rename(%s -> %s): %s", src, dst, err.Error())
		return err
	}
	v.invalidateEntryCache(fhSrc)
	v.invalidateEntryCache(fhDst)
	return nil
}

// Symlink creates a symbolic link refer to src
func (v *Target) Symlink(src, dst string) error {
	dst = filepath.Clean(dst)
	parentDst, dstName := filepath.Split(dst)
	if dstName == "" || dstName == "/" || dstName == "." {
		return os.ErrInvalid
	}

	_, fhDst, err := v.Lookup(parentDst)
	if err != nil {
		return err
	}

	return v.symlink(src, fhDst, dstName)
}

func (v *Target) symlink(srcPath string, fhDst []byte, dst string) error {
	type SymlinkArgs struct {
		rpc.Header
		Link             Diropargs3
		Sattr            Sattr3
		SymbolicLinkData string
	}

	_, err := v.call(&SymlinkArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    Nfs3Prog,
			Vers:    Nfs3Vers,
			Proc:    NFSProc3Symlink,
			Cred:    v.auth,
			Verf:    rpc.AuthNull,
		},
		Link: Diropargs3{
			FH:       fhDst,
			Filename: dst,
		},
		SymbolicLinkData: srcPath,
	})

	if err != nil {
		util.Debugf("symlink(%s -> %s): %s", srcPath, dst, err.Error())
		return err
	}
	return nil
}
