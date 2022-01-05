package proxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"sync"
)

type Config struct {
	Proxy ProxyConfig `json:"proxy"`
}

type ProxyConfig struct {
	Listen string `json:"listen"`
	Encrypt bool `json:"encrypt"`
	Decrypt bool `json:"decrypt"`
	Key string `json:"key"`
	Upstream UpstreamConfig `json:"upstream"`
}

type UpstreamConfig struct {
	Name string `json:"name"`
	Url string `json:"url"`
	Encrypt bool `json:"encrypt"`
	Decrypt bool `json:"decrypt"`
	Key string `json:"key"`
}

type Proxy struct {
	config ProxyConfig
}

func NewProxy(cfg *Config) *Proxy {
	proxy := Proxy{config: cfg.Proxy}
	return &proxy
}

func (proxy *Proxy) Run() {
	server, err := net.Listen("tcp", proxy.config.Listen)
	if err != nil {
		log.Fatal(err.Error())
	}
	log.Printf("start to listen %s\n", proxy.config.Listen)


	for {
		s_conn, err := server.Accept()
		if err != nil {
			log.Printf("log failed: %s\n", err.Error())
			continue
		}
		log.Printf("login IP %s success\n", s_conn.RemoteAddr())

		d_tcpAddr, _ := net.ResolveTCPAddr("tcp4", proxy.config.Upstream.Url)
		d_conn, err := net.DialTCP("tcp", nil, d_tcpAddr)
		if err != nil {
			log.Printf("log failed: %s\n", err.Error())
			continue
		}
		go func() {
			err := proxy.handleTCPClient(s_conn, d_conn)
			if err != nil {
				log.Printf("handle err: %s\n", err.Error())
			}
		}()
	}

}

func (proxy *Proxy) handleTCPClient(srcConn , dstConn net.Conn) error {
	var wait sync.WaitGroup
	wait.Add(2)

	go proxy.handleUpstream(srcConn, dstConn, &wait)
	go proxy.handleDownstream(srcConn, dstConn, &wait)
	wait.Wait()
	log.Printf("IP %s login exit",srcConn.RemoteAddr())
	return nil
}

func (proxy *Proxy) handleUpstream(srcConn , dstConn net.Conn, wait *sync.WaitGroup) error {
	defer srcConn.Close()
	defer dstConn.Close()
	defer wait.Done()

	key := []byte(proxy.config.Upstream.Key)

	if proxy.config.Upstream.Encrypt {
		srcReader := bufio.NewReader(srcConn)
		for {
			readString, err := srcReader.ReadString('\n')
			if err != nil {
				return err
			}
			//log.Printf("\nslen: %d\n", len(readString))
			readString = readString[:len(readString)-1]
			//fmt.Printf("upstream encrypt: %v|",readString)
			//result, err := remoteReader.ReadString('\n')
			//start := time.Now() // 获取当前时间

			encryptMsg, _ := encrypt(key, readString)

			//elapsed := time.Since(start)
			//fmt.Println("\n编码函数执行完成耗时：", elapsed)
			//readString = encryptMsg + "\n"
			//fmt.Print(readString + "|")
			dstConn.Write([]byte(encryptMsg))
			dstConn.Write([]byte("\n"))

		}

	} else if proxy.config.Upstream.Decrypt{
		srcReader := bufio.NewReader(srcConn)
		for {

			readString, err := srcReader.ReadString('\n')
			if err != nil {
				return err
			}
			//log.Printf("\nslen: %d\n", len(readString))
			readString = readString[:len(readString)-1]
			//result, err := remoteReader.ReadString('\n')
			//start := time.Now() // 获取当前时间
			msg, _ := Decrypt(key, readString)
			//elapsed := time.Since(start)
			//fmt.Println("\n解码函数执行完成耗时：", elapsed)
			//fmt.Print(msg +"|||")
			//readString = msg + "\n"
			dstConn.Write([]byte(msg))
			dstConn.Write([]byte("\n"))

		}
	} else {
		io.Copy(dstConn, srcConn)
	}
	return nil
}

func (proxy *Proxy) handleDownstream(srcConn , dstConn net.Conn, wait *sync.WaitGroup) error {
	defer srcConn.Close()
	defer dstConn.Close()
	defer wait.Done()

	key := []byte(proxy.config.Key)


	if proxy.config.Encrypt {
		dstReader := bufio.NewReader(dstConn)
		for {
			readBytes, err := dstReader.ReadBytes('\n')
			//fmt.Println(readBytes)
			readString := string(readBytes)
			//readString, err := dstReader.ReadString('\n')
			if err != nil {
				return err
			}
			//log.Printf("\nslen: %d\n", len(readString))
			readString = readString[:len(readString)-1]
			//fmt.Print("downstream %s", readString)
			//result, err := remoteReader.ReadString('\n')
			encryptMsg, _ := encrypt(key, readString)
			//readString = encryptMsg + "\n"
			srcConn.Write([]byte(encryptMsg))
			srcConn.Write([]byte("\n"))

		}

	} else if proxy.config.Decrypt {
		//log.Printf("start Decrypt DownStream")
		dstReader := bufio.NewReader(dstConn)
		for {
			readString, err := dstReader.ReadString('\n')
			if err != nil {
				//log.Printf(err.Error())
				return err
			}
			//log.Printf("%s\n", readString)
			//log.Printf("\nslen: %d\n", len(readString))
			readString = readString[:len(readString)-1]

			//result, err := remoteReader.ReadString('\n')
			msg, _ := Decrypt(key, readString)
			//fmt.Printf("downstream decrypt: %v|",msg)
			//fmt.Print(msg)
			//readString = msg + "\n"
			srcConn.Write([]byte(msg))
			srcConn.Write([]byte("\n"))

		}
	} else {
		io.Copy(srcConn, dstConn)
	}

	return nil
}
