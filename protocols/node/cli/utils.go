package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"time"

	moody "github.com/taubyte/go-interfaces/moody"
	keypair "github.com/taubyte/p2p/keypair"
	"github.com/taubyte/utils/fs/file"
	"github.com/urfave/cli/v2"
)

func UtilsGenKey() error {
	fmt.Println(base64.StdEncoding.EncodeToString(keypair.NewRaw()))
	return nil
}

func UtilsPing(c *cli.Context, pid string) error {
	StartNode(c)

	if err := waitForSwarm(); err != nil {
		return err
	}

	Logger.Debug(moody.Object{"message": fmt.Sprintf("SWARM :%v", Node.Peer().Peerstore().Peers())})

	_, rtt, err := Node.Ping(pid, 4)
	time.Sleep(time.Second * 1)
	if err != nil {
		return err
	}

	fmt.Printf("Ping of %s successful, took %dms\n", pid, rtt/time.Millisecond)
	return nil
}

func UtilsAddr(c *cli.Context) error {
	StartNode(c)
	time.Sleep(5 * time.Second)
	for _, addr := range Node.Peer().Addrs() {
		fmt.Println(addr.String())
	}
	return nil
}

/*func UtilsRoute(pid string) error {
	initNode(4001)
	for _, addr := range Node.Peer().Peerstore().Peers() .Peerstore().Addrs(pid) {
		fmt.Println(addr.String())
	}
	return nil
}*/

func UtilsPeers(c *cli.Context) error {
	StartNode(c)
	for _, pid := range Node.Peer().Peerstore().Peers() {
		fmt.Println(pid)
	}
	return nil
}

func UtilsProvide(c *cli.Context, paths []string) error {
	StartNode(c)

	for _, path := range paths {
		if !file.Exists(path) {
			return fmt.Errorf("file `%s` does not exist", path)
		}
	}

	files := make(map[string]string)

	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("reading file `%s` failed with %s", path, err.Error())
		}
		nid, err := Node.AddFile(f)
		if err != nil {
			return fmt.Errorf("adding file `%s` failed with %s", path, err.Error())
		}
		files[path] = nid
	}
	nodeDone()

	return nil
}

func UtilsFetch(c *cli.Context, ctx context.Context, cid string) error {
	StartNode(c)

	f, err := Node.GetFile(ctx, cid)
	if err != nil {
		fmt.Println(err)
		return err
	}

	buf := make([]byte, 1024)
	for {
		n, _ := f.Read(buf)
		if n <= 0 {
			break
		}
		_, err = os.Stdout.Write(buf[:n])
		if err != nil {
			return err
		}
		if err == io.EOF {
			break
		}
	}

	return nil
}
