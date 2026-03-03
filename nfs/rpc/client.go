// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package rpc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lyp256/go-nfs-client/nfs/util"
	"github.com/lyp256/go-nfs-client/nfs/xdr"
)

const (
	MsgAccepted = iota
	MsgDenied
)

const (
	Success = iota
	ProgUnavail
	ProgMismatch
	ProcUnavail
	GarbageArgs
	SystemErr
)

const (
	RpcMismatch = iota
)

var xid uint32

func init() {
	// seed the XID (which is set by the client)
	xid = rand.New(rand.NewSource(time.Now().UnixNano())).Uint32()
}

var DefaultReadTimeout = time.Second * 5

type Client struct {
	ctx    context.Context
	cancel context.CancelFunc

	*tcpTransport
	sync.Mutex
	network string
	addr    string
	replies map[uint32]chan io.ReadSeeker
}

func DialTCP(network string, addr string) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		ctx:     ctx,
		cancel:  cancel,
		Mutex:   sync.Mutex{},
		network: network,
		addr:    addr,
		replies: make(map[uint32]chan io.ReadSeeker),
	}
	if t, err := c.connect(); err != nil {
		return nil, err
	} else {
		c.tcpTransport = t
	}
	go c.receive()
	return c, nil
}

type message struct {
	Xid     uint32
	Msgtype uint32
	Body    interface{}
}

func (c *Client) Context() context.Context {
	return c.ctx
}

func (c *Client) receive() {
loop:
	for {
		c.Lock()
		select {
		case <-c.ctx.Done():
			break loop
		default:
		}
		t := c.tcpTransport
		c.Unlock()
		if t == nil {
			var err error
			t, err = c.connect()
			if err != nil {
				time.Sleep(time.Millisecond * 100)
				continue
			}
			c.Lock()
			c.tcpTransport = t
			c.Unlock()
		}
		res, err := t.recv()
		if err != nil {
			util.Debugf("nfs rpc: recv got error: %s", err)
			c.disconnect()
			continue
		}
		xid, err := xdr.ReadUint32(res)
		if err != nil {
			c.disconnect()
			continue
		}

		c.Lock()
		r, ok := c.replies[xid]
		c.Unlock()
		if ok {
			r <- res
		} else {
			util.Errorf("received unexpected response with xid: %x", xid)
		}
	}
}

func (c *Client) connect() (*tcpTransport, error) {
	a, err := net.ResolveTCPAddr(c.network, c.addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTCP(a.Network(), nil, a)
	if err != nil {
		return nil, err
	}
	util.Debugf("connected with local %s -> remote %s", conn.LocalAddr(), c.addr)
	return &tcpTransport{
		r:  bufio.NewReader(conn),
		wc: conn,
	}, nil
}

func (c *Client) disconnect() {
	c.Lock()
	defer c.Unlock()
	if c.tcpTransport != nil {
		c.tcpTransport.Close()
		c.tcpTransport = nil
	}
	for _, r := range c.replies {
		close(r)
	}
	c.replies = make(map[uint32]chan io.ReadSeeker)
}

func (c *Client) Close() {
	c.Lock()
	c.cancel()
	c.Unlock()
	c.disconnect()
}

func (c *Client) Call(call interface{}) (io.ReadSeeker, error) {
	msg := &message{
		Xid:  atomic.AddUint32(&xid, 1),
		Body: call,
	}
	w := new(bytes.Buffer)
	if err := xdr.Write(w, msg); err != nil {
		return nil, err
	}

	retries := 0
	garbage := false
retry:
	retries++
	if retries > 100 {
		return nil, errors.New("disconnected")
	}

	c.Lock()
	if c.tcpTransport == nil {
		c.Unlock()
		time.Sleep(time.Millisecond * 100)
		goto retry
	}
	if _, err := c.Write(w.Bytes()); err != nil {
		c.Unlock()
		c.disconnect()
		goto retry
	}
	reply := make(chan io.ReadSeeker)
	c.replies[msg.Xid] = reply
	c.Unlock()

	var res io.ReadSeeker
	select {
	case res = <-reply:
	case <-time.After(DefaultReadTimeout):
	}

	c.Lock()
	delete(c.replies, msg.Xid)
	c.Unlock()

	if res == nil {
		goto retry
	}

	mtype, err := xdr.ReadUint32(res)
	if err != nil {
		return nil, err
	}
	if mtype != 1 {
		return nil, fmt.Errorf("message as not a reply: %d", mtype)
	}

	status, err := xdr.ReadUint32(res)
	if err != nil {
		return nil, err
	}

	switch status {
	case MsgAccepted:

		// padding
		_, err = xdr.ReadUint32(res)
		if err != nil {
			return nil, fmt.Errorf("rpc: failed to read padding: %w", err)
		}

		opaque_len, err := xdr.ReadUint32(res)
		if err != nil {
			return nil, fmt.Errorf("rpc: failed to read opaque length: %w", err)
		}

		_, err = res.Seek(int64(opaque_len), io.SeekCurrent)
		if err != nil {
			return nil, fmt.Errorf("rpc: failed to seek opaque data: %w", err)
		}

		acceptStatus, _ := xdr.ReadUint32(res)

		switch acceptStatus {
		case Success:
			return res, nil
		case ProgUnavail:
			return nil, fmt.Errorf("rpc: PROG_UNAVAIL - server does not recognize the program number")
		case ProgMismatch:
			return nil, fmt.Errorf("rpc: PROG_MISMATCH - program version does not exist on the server")
		case ProcUnavail:
			return nil, fmt.Errorf("rpc: PROC_UNAVAIL - unrecognized procedure number")
		case GarbageArgs:
			// emulate Linux behaviour for GARBAGE_ARGS
			if !garbage {
				util.Debugf("Retrying on GARBAGE_ARGS per linux semantics")
				garbage = true
				goto retry
			}

			return nil, fmt.Errorf("rpc: GARBAGE_ARGS - rpc arguments cannot be XDR decoded")
		case SystemErr:
			return nil, fmt.Errorf("rpc: SYSTEM_ERR - unknown error on server")
		default:
			return nil, fmt.Errorf("rpc: unknown accepted status error: %d", acceptStatus)
		}

	case MsgDenied:
		rejectStatus, _ := xdr.ReadUint32(res)
		switch rejectStatus {
		case RpcMismatch:
			return nil, fmt.Errorf("rpc: RPC_MISMATCH - RPC version mismatch")
		default:
			return nil, fmt.Errorf("rejectedStatus was not valid: %d", rejectStatus)
		}

	default:
		return nil, fmt.Errorf("message status was not valid: %d", status)
	}
}
