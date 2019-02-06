package packetchan

import "net"

type PacketChan interface {
	ReadChan() chan BufNAddr
	WriteChan() chan BufNAddr
	Close() error
}

type packetChan struct {
	conn           net.PacketConn
	readBufferSize uint
	writeTo        net.Addr
	readChan       chan BufNAddr
	writeChan      chan BufNAddr
}

type BufNAddr struct {
	Buf  []byte
	Addr net.Addr
}

func (conn packetChan) ReadChan() chan BufNAddr {
	return conn.readChan
}

func (conn packetChan) WriteChan() chan BufNAddr {
	return conn.writeChan
}

func (conn packetChan) Close() error {
	return conn.conn.Close()
}

func CreateChan(conn net.PacketConn, readBufferSize uint, writeTo net.Addr) (PacketChan, error) {
	var result = packetChan{
		conn,
		readBufferSize,
		writeTo,
		make(chan BufNAddr),
		make(chan BufNAddr),
	}
	go func() {
		for {
			var buf = make([]byte, result.readBufferSize)
			var size, addr, err = result.conn.ReadFrom(buf)
			if size > 0 {
				result.readChan <- BufNAddr{buf[:size], addr}
			}
			if err != nil {
				result.Close()
				break
			}
		}
	}()
	go func() {
		for {
			var bufNAddr = <-result.writeChan
			var _, err = conn.WriteTo(bufNAddr.Buf, bufNAddr.Addr)
			if err != nil {
				result.Close()
				break
			}
		}
	}()
	return result, nil
}
