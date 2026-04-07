package main

import (
	"fmt"
	"log"
	"time"

	"github.com/lyp256/go-nfs-client/nfs"
	"github.com/lyp256/go-nfs-client/nfs/rpc"
	"github.com/lyp256/go-nfs-client/nfs/util"
)

func main() {
	util.DefaultLogger.SetDebug(true)

	host := "192.168.5.172"
	target := "/nfs/test"

	mount, err := nfs.DialMount(host, time.Second*10)
	if err != nil {
		log.Fatalf("unable to dial MOUNT service: %v", err)
	}
	defer mount.Close()

	auth := rpc.NewAuthUnix("root", 0, 0)

	v, err := mount.Mount(target, auth.Auth())
	if err != nil {
		log.Fatalf("unable to mount volume: %v", err)
	}
	defer v.Close()

	// 清理之前创建的测试目录
	fmt.Println("=== 清理旧测试数据 ===")
	v.RemoveAll("testall")

	// 测试1: 创建多级目录
	fmt.Println("\n=== 测试1: 创建多级目录 (a/b/c) ===")
	testDir := "testall/a/b/c"
	fh, err := v.MkdirAll(testDir, 0755)
	if err != nil {
		log.Printf("MkdirAll(%q) error: %v", testDir, err)
	} else {
		fmt.Printf("MkdirAll(%q) success, fh=%x\n", testDir, fh)
	}

	// 测试2: 创建已存在的目录 (不应报错)
	fmt.Println("\n=== 测试2: 创建已存在的目录 (testall/a/b) ===")
	testDir2 := "testall/a/b"
	fh, err = v.MkdirAll(testDir2, 0755)
	if err != nil {
		log.Printf("MkdirAll(%q) error: %v", testDir2, err)
	} else {
		fmt.Printf("MkdirAll(%q) success, fh=%x\n", testDir2, fh)
	}

	// 测试3: 在已存在的目录上继续创建
	fmt.Println("\n=== 测试3: 在已存在的目录上继续创建 (testall/a/b/d/e) ===")
	testDir3 := "testall/a/b/d/e"
	fh, err = v.MkdirAll(testDir3, 0755)
	if err != nil {
		log.Printf("MkdirAll(%q) error: %v", testDir3, err)
	} else {
		fmt.Printf("MkdirAll(%q) success, fh=%x\n", testDir3, fh)
	}

	// 清理
	fmt.Println("\n=== 清理测试数据 ===")
	v.RemoveAll("testall")
}
