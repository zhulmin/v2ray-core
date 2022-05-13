package shadowsocks_2022

import (
	"context"
	"encoding/base64"
	"io"
	"runtime"
	"strings"
	"time"

	C "github.com/sagernet/sing/common"
	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/random"
	"github.com/sagernet/sing/common/rw"
	"github.com/sagernet/sing/protocol/shadowsocks"
	"github.com/sagernet/sing/protocol/shadowsocks/shadowaead_2022"

	"github.com/v2fly/v2ray-core/v5/common"
	"github.com/v2fly/v2ray-core/v5/common/buf"
	"github.com/v2fly/v2ray-core/v5/common/net"
	"github.com/v2fly/v2ray-core/v5/common/net/packetaddr"
	"github.com/v2fly/v2ray-core/v5/common/session"
	"github.com/v2fly/v2ray-core/v5/transport"
	"github.com/v2fly/v2ray-core/v5/transport/internet"
)

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}

type Outbound struct {
	ctx    context.Context
	server net.Destination
	method shadowsocks.Method
}

func NewClient(ctx context.Context, config *ClientConfig) (*Outbound, error) {
	o := &Outbound{
		ctx: ctx,
		server: net.Destination{
			Address: config.Address.AsAddress(),
			Port:    net.Port(config.Port),
			Network: net.Network_TCP,
		},
	}
	if C.Contains(shadowaead_2022.List, config.Method) {
		if config.Key == "" {
			return nil, newError("missing psk")
		}
		var pskList [][]byte
		for _, ks := range strings.Split(config.Key, ":") {
			psk, err := base64.StdEncoding.DecodeString(ks)
			if err != nil {
				return nil, newError("decode key ", ks).Base(err)
			}
			pskList = append(pskList, psk)
		}
		var rng io.Reader = random.Default
		if config.ExperimentReducedIvHeadEntropy {
			rng = &shadowsocks.ReducedEntropyReader{
				Reader: rng,
			}
		}
		method, err := shadowaead_2022.New(config.Method, pskList, "", rng)
		if err != nil {
			return nil, newError("create method").Base(err)
		}
		o.method = method
	} else {
		return nil, newError("unknown method ", config.Method)
	}
	return o, nil
}

func (o *Outbound) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbound := session.OutboundFromContext(ctx)
	if outbound == nil || !outbound.Target.IsValid() {
		return newError("target not specified")
	}
	destination := outbound.Target
	network := destination.Network

	newError("tunneling request to ", destination, " via ", o.server.NetAddr()).WriteToLog(session.ExportIDToError(ctx))

	serverDestination := o.server
	serverDestination.Network = network
	connection, err := dialer.Dial(ctx, serverDestination)
	if err != nil {
		return newError("failed to connect to server").Base(err)
	}

	if network == net.Network_TCP {
		serverConn := o.method.DialEarlyConn(connection, SingDestination(destination))
		var handshake bool
		if timeoutReader, isTimeoutReader := link.Reader.(buf.TimeoutReader); isTimeoutReader {
			mb, err := timeoutReader.ReadMultiBufferTimeout(time.Millisecond * 100)
			if err != nil && err != buf.ErrNotTimeoutReader && err != buf.ErrReadTimeout {
				return newError("read payload").Base(err)
			}
			_payload := B.StackNew()
			payload := C.Dup(_payload)
			for {
				payload.FullReset()
				nb, n := buf.SplitBytes(mb, payload.FreeBytes())
				if n > 0 {
					payload.Truncate(n)
					_, err = serverConn.Write(payload.Bytes())
					if err != nil {
						return newError("write payload").Base(err)
					}
					newError("write payload handshake").AtDebug().WriteToLog()
					handshake = true
				}
				if nb.IsEmpty() {
					break
				} else {
					mb = nb
				}
			}
			runtime.KeepAlive(_payload)
		}
		if !handshake {
			newError("write padding handshake").AtDebug().WriteToLog()
			_, err = serverConn.Write(nil)
			if err != nil {
				return newError("client handshake").Base(err)
			}
		}
		conn := &pipeConnWrapper{
			R: &buf.BufferedReader{Reader: link.Reader},
			W: link.Writer,
		}
		return rw.CopyConn(ctx, conn, serverConn)
	} else {
		serverConn := o.method.DialPacketConn(connection)
		if inboundConn, err := packetaddr.ToPacketAddrConn(link, destination); err == nil {
			return N.CopyNetPacketConn(ctx, serverConn, inboundConn)
		}
		return newError("unrecognized connection")
	}
}

func SingDestination(destination net.Destination) M.Socksaddr {
	var addr M.Socksaddr
	switch destination.Address.Family() {
	case net.AddressFamilyDomain:
		addr.Fqdn = destination.Address.Domain()
	default:
		addr.Addr = M.AddrFromIP(destination.Address.IP())
	}
	addr.Port = uint16(destination.Port)
	return addr
}

type pipeConnWrapper struct {
	R io.Reader
	W buf.Writer
	net.Conn
}

func (w *pipeConnWrapper) Close() error {
	common.Interrupt(w.R)
	common.Interrupt(w.W)
	return nil
}

func (w *pipeConnWrapper) Read(b []byte) (n int, err error) {
	return w.R.Read(b)
}

func (w *pipeConnWrapper) Write(p []byte) (n int, err error) {
	n = len(p)
	var mb buf.MultiBuffer
	pLen := len(p)
	for pLen > 0 {
		buffer := buf.New()
		if pLen > buf.Size {
			_, err = buffer.Write(p[:buf.Size])
			p = p[buf.Size:]
		} else {
			buffer.Write(p)
		}
		pLen -= int(buffer.Len())
		mb = append(mb, buffer)
	}
	err = w.W.WriteMultiBuffer(mb)
	if err != nil {
		n = 0
		buf.ReleaseMulti(mb)
	}
	return
}
