//+build linux

package main

//go:generate go run cmd/genversion/main.go -package main -out version.go

import (
	"context"
	"flag"
	"fmt"
	"os"

	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"dev.hexasoftware.com/hxs/cloudmount/fs/gdrivefs"
	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseutil"

	"dev.hexasoftware.com/hxs/prettylog"
	//_ "github.com/icattlecoder/godaemon" // No reason
)

var (
	Name = "cloudmount"
	log  = prettylog.New("main")
)

func main() {
	var daemonize bool
	var verboselog bool
	var clouddrive string

	prettylog.Global()
	// getClient
	fmt.Printf("%s-%s\n\n", Name, Version)

	flag.StringVar(&clouddrive, "t", "gdrive", "which cloud service to use [gdrive]")
	flag.BoolVar(&daemonize, "d", false, "Run app in background")
	flag.BoolVar(&verboselog, "v", false, "Verbose log")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] MOUNTPOINT\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n")
	}
	flag.Parse()

	if len(flag.Args()) < 1 {
		flag.Usage()
		//fmt.Println("Usage:\n gdrivemount [-d] [-v] MOUNTPOINT")
		return
	}

	driveFS := gdrivefs.NewGDriveFS() // there can be some interaction before daemon

	// Daemon
	if daemonize {
		subArgs := []string{}
		for _, arg := range os.Args[1:] {
			if arg == "-d" { // ignore daemon flag
				continue
			}
			subArgs = append(subArgs, arg)
		}

		cmd := exec.Command(os.Args[0], subArgs...)
		cmd.Start()
		fmt.Println("[PID]", cmd.Process.Pid)
		os.Exit(0)
		return
	}

	//////////////
	// Server
	/////////
	ctx := context.Background()
	server := fuseutil.NewFileSystemServer(driveFS)
	mountPath := flag.Arg(0)

	var err error
	var mfs *fuse.MountedFileSystem

	if verboselog {
		mfs, err = fuse.Mount(mountPath, server, &fuse.MountConfig{DebugLogger: prettylog.New("fuse"), ErrorLogger: prettylog.New("fuse-err")})
	} else {
		mfs, err = fuse.Mount(mountPath, server, &fuse.MountConfig{})
	}
	if err != nil {
		log.Fatal("Failed mounting path", flag.Arg(0))
	}

	// Signal handling to refresh Drives
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGUSR1, syscall.SIGHUP, syscall.SIGINT, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range sigs {
			log.Println("Signal:", sig)
			switch sig {
			case syscall.SIGUSR1:
				log.Println("Manually Refresh drive")
				go driveFS.Refresh()
			case syscall.SIGHUP:
				log.Println("GC")
				mem := runtime.MemStats{}
				runtime.ReadMemStats(&mem)
				log.Printf("Mem: %.2fMB", float64(mem.Alloc)/1024/1024)
				runtime.GC()

				runtime.ReadMemStats(&mem)
				log.Printf("After gc: Mem: %.2fMB", float64(mem.Alloc)/1024/1024)

			case os.Interrupt:
				log.Println("Graceful unmount")
				fuse.Unmount(mountPath)
				os.Exit(1)
			case syscall.SIGTERM:
				log.Println("Graceful unmount")
				fuse.Unmount(mountPath)
				os.Exit(1)
			}

		}
	}()

	if err := mfs.Join(ctx); err != nil {
		log.Fatalf("Joining: %v", err)
	}

}