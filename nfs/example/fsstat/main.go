// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/lyp256/go-nfs-client/nfs"
	"github.com/lyp256/go-nfs-client/nfs/rpc"
	"github.com/lyp256/go-nfs-client/nfs/util"
)

func main() {
	util.DefaultLogger.SetDebug(false)
	if len(os.Args) != 2 {
		fmt.Printf("%s <host>:<target path>\n", os.Args[0])
		os.Exit(-1)
	}

	b := strings.Split(os.Args[1], ":")
	host := b[0]
	target := b[1]

	mount, err := nfs.DialMount(host, time.Second)
	if err != nil {
		log.Fatalf("unable to dial MOUNT service: %v", err)
	}
	defer mount.Close()

	auth := rpc.NewAuthUnix("hasselhoff", 1001, 1001)

	v, err := mount.Mount(target, auth.Auth())
	if err != nil {
		log.Fatalf("unable to mount volume: %v", err)
	}
	defer v.Close()

	fsstat, err := v.FSStat()
	if err != nil {
		log.Fatalf("FSStat error: %v", err)
	}

	fmt.Println("=== FSStat 结果 ===")
	fmt.Printf("TotalBytes:   %d (%.2f TB)\n", fsstat.TotalBytes, float64(fsstat.TotalBytes)/1e12)
	fmt.Printf("FreeBytes:    %d (%.2f GB)\n", fsstat.FreeBytes, float64(fsstat.FreeBytes)/1e9)
	fmt.Printf("AvailBytes:   %d (%.2f GB)\n", fsstat.AvailBytes, float64(fsstat.AvailBytes)/1e9)
	fmt.Printf("TotalFiles:   %d\n", fsstat.TotalFiles)
	fmt.Printf("FreeFiles:    %d\n", fsstat.FreeFiles)
	fmt.Printf("AvailFiles:   %d\n", fsstat.AvailFiles)

	// 计算已使用量
	usedBytes := fsstat.TotalBytes - fsstat.FreeBytes
	usedFiles := fsstat.TotalFiles - fsstat.FreeFiles

	fmt.Println("\n=== 计算已使用量 ===")
	fmt.Printf("UsedBytes:    %d (%.2f GB)\n", usedBytes, float64(usedBytes)/1e9)
	fmt.Printf("UsedFiles:    %d\n", usedFiles)

	if fsstat.TotalBytes > 0 {
		usagePercent := float64(usedBytes) / float64(fsstat.TotalBytes) * 100
		fmt.Printf("\n使用率: %.2f%%\n", usagePercent)
	}

	fmt.Println("\n=== 对比 df 命令 ===")
	fmt.Println("df 显示:")
	fmt.Println("  Total:  7999426920448 (约 8 TB)")
	fmt.Println("  Used:   182486827008 (约 170 GB)")
	fmt.Println("  Avail:  7816940093440 (约 7.82 TB)")
}
