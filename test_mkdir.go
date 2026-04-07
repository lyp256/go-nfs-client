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
	v.RemoveAll("testmkdir123")

	// 测试1: 在根目录创建目录
	fmt.Println("\n=== 测试1: 在根目录创建目录 ===")
	testDir := "testmkdir123"
	fh, err := v.Mkdir(testDir, 0755)
	if err != nil {
		log.Printf("Mkdir(%q) error: %v", testDir, err)
	} else {
		fmt.Printf("Mkdir(%q) success, fh=%x\n", testDir, fh)
	}

	// 测试2: 在已存在的目录下创建子目录
	fmt.Println("\n=== 测试2: 在已存在的目录下创建子目录 ===")
	testDir2 := "testmkdir123/subdir"
	fh, err = v.Mkdir(testDir2, 0755)
	if err != nil {
		log.Printf("Mkdir(%q) error: %v", testDir2, err)
	} else {
		fmt.Printf("Mkdir(%q) success, fh=%x\n", testDir2, fh)
	}

	// 测试3: 在不存在的目录下创建目录 (应该失败)
	fmt.Println("\n=== 测试3: 在不存在的目录下创建目录 ===")
	testDir3 := "nonexistent/newdir"
	fh, err = v.Mkdir(testDir3, 0755)
	if err != nil {
		log.Printf("Mkdir(%q) error: %v", testDir3, err)
	} else {
		fmt.Printf("Mkdir(%q) success, fh=%x\n", testDir3, fh)
	}

	// 测试4: 使用绝对路径格式
	fmt.Println("\n=== 测试4: 使用绝对路径格式 ===")
	testDir4 := "/testmkdir456"
	fh, err = v.Mkdir(testDir4, 0755)
	if err != nil {
		log.Printf("Mkdir(%q) error: %v", testDir4, err)
	} else {
		fmt.Printf("Mkdir(%q) success, fh=%x\n", testDir4, fh)
	}

	// 清理
	v.RemoveAll("testmkdir123")
	v.Remove("testmkdir456")
}
