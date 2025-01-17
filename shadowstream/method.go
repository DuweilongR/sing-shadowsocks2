package shadowstream

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	math_rand "math/rand"
	"net"
	"os"
	"time"

	C "github.com/DuweilongR/sing-shadowsocks2/cipher"
	"github.com/DuweilongR/sing-shadowsocks2/internal/legacykey"
	"github.com/metacubex/mihomo/log"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/aead/chacha20/chacha"
	"golang.org/x/crypto/chacha20"
)

var MethodList = []string{
	"aes-128-ctr",
	"aes-192-ctr",
	"aes-256-ctr",
	"aes-128-cfb",
	"aes-192-cfb",
	"aes-256-cfb",
	"rc4-md5",
	"chacha20-ietf",
	"xchacha20",
	"chacha20",
}

func init() {
	C.RegisterMethod(MethodList, NewMethod)
}

type Method struct {
	keyLength          int
	saltLength         int
	encryptConstructor func(key []byte, salt []byte) (cipher.Stream, error)
	decryptConstructor func(key []byte, salt []byte) (cipher.Stream, error)
	key                []byte

	password   string
	methodName string
	ctx        context.Context
	option     C.MethodOptions
}

func ivGenerator(ivSize int) ([]byte, error) {
	iv := make([]byte, ivSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	return iv, nil
}

func NewMethod_L(ctx context.Context, methodName string, options C.MethodOptions) (*Method, error) {
	m := &Method{}
	m.methodName = methodName
	m.ctx = ctx
	m.option = options
	m.password = options.Password
	switch methodName {
	case "aes-128-ctr":
		m.keyLength = 16
		m.saltLength = aes.BlockSize
		m.encryptConstructor = blockStream(aes.NewCipher, cipher.NewCTR)
		m.decryptConstructor = blockStream(aes.NewCipher, cipher.NewCTR)
	case "aes-192-ctr":
		m.keyLength = 24
		m.saltLength = aes.BlockSize
		m.encryptConstructor = blockStream(aes.NewCipher, cipher.NewCTR)
		m.decryptConstructor = blockStream(aes.NewCipher, cipher.NewCTR)
	case "aes-256-ctr":
		m.keyLength = 32
		m.saltLength = aes.BlockSize
		m.encryptConstructor = blockStream(aes.NewCipher, cipher.NewCTR)
		m.decryptConstructor = blockStream(aes.NewCipher, cipher.NewCTR)
	case "aes-128-cfb":
		m.keyLength = 16
		m.saltLength = aes.BlockSize
		m.encryptConstructor = blockStream(aes.NewCipher, cipher.NewCFBEncrypter)
		m.decryptConstructor = blockStream(aes.NewCipher, cipher.NewCFBDecrypter)
	case "aes-192-cfb":
		m.keyLength = 24
		m.saltLength = aes.BlockSize
		m.encryptConstructor = blockStream(aes.NewCipher, cipher.NewCFBEncrypter)
		m.decryptConstructor = blockStream(aes.NewCipher, cipher.NewCFBDecrypter)
	case "aes-256-cfb":
		m.keyLength = 32
		m.saltLength = aes.BlockSize
		m.encryptConstructor = blockStream(aes.NewCipher, cipher.NewCFBEncrypter)
		m.decryptConstructor = blockStream(aes.NewCipher, cipher.NewCFBDecrypter)
	case "rc4-md5":
		m.keyLength = 16
		m.saltLength = 16
		m.encryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			h := md5.New()
			h.Write(key)
			h.Write(salt)
			return rc4.NewCipher(h.Sum(nil))
		}
		m.decryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			h := md5.New()
			h.Write(key)
			h.Write(salt)
			return rc4.NewCipher(h.Sum(nil))
		}
	case "chacha20-ietf":
		m.keyLength = chacha20.KeySize
		m.saltLength = chacha20.NonceSize
		m.encryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			return chacha20.NewUnauthenticatedCipher(key, salt)
		}
		m.decryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			return chacha20.NewUnauthenticatedCipher(key, salt)
		}
	case "xchacha20":
		m.keyLength = chacha20.KeySize
		m.saltLength = chacha20.NonceSizeX
		m.encryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			return chacha20.NewUnauthenticatedCipher(key, salt)
		}
		m.decryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			return chacha20.NewUnauthenticatedCipher(key, salt)
		}
	case "chacha20":
		m.keyLength = chacha.KeySize
		m.saltLength = chacha.NonceSize
		m.encryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			return chacha.NewCipher(salt, key, 20)
		}
		m.decryptConstructor = func(key []byte, salt []byte) (cipher.Stream, error) {
			return chacha.NewCipher(salt, key, 20)
		}
	default:
		return nil, os.ErrInvalid
	}
	if len(options.Key) == m.keyLength {
		m.key = options.Key
	} else if len(options.Key) > 0 {
		return nil, E.New("bad key length, required ", m.keyLength, ", got ", len(options.Key))
	} else if options.Password != "" {
		m.key = legacykey.Key([]byte(options.Password), m.keyLength)
	} else {
		return nil, C.ErrMissingPassword
	}
	return m, nil
}

func NewMethod(ctx context.Context, methodName string, options C.MethodOptions) (C.Method, error) {
	return NewMethod_L(ctx, methodName, options)
}

func blockStream(blockCreator func(key []byte) (cipher.Block, error), streamCreator func(block cipher.Block, iv []byte) cipher.Stream) func([]byte, []byte) (cipher.Stream, error) {
	return func(key []byte, iv []byte) (cipher.Stream, error) {
		block, err := blockCreator(key)
		if err != nil {
			return nil, err
		}
		return streamCreator(block, iv), err
	}
}

func (m *Method) DialConn(conn net.Conn, destination M.Socksaddr) (net.Conn, error) {
	ssConn := &clientConn{
		ExtendedConn: bufio.NewExtendedConn(conn),
		method:       m,
		destination:  destination,
	}
	return ssConn, common.Error(ssConn.Write(nil))
}

func (m *Method) DialEarlyConn(conn net.Conn, destination M.Socksaddr) net.Conn {
	return &clientConn{
		ExtendedConn: bufio.NewExtendedConn(conn),
		method:       m,
		destination:  destination,
	}
}

func (m *Method) DialPacketConn(conn net.Conn) N.NetPacketConn {
	return &clientPacketConn{
		ExtendedConn: bufio.NewExtendedConn(conn),
		method:       m,
	}
}

const MAX_HEADER_AND_IV_SIZE = 256
const bufSize = MAX_HEADER_AND_IV_SIZE

var (
	lengthMethod = "aes-256-cfb"
	//lengthIV       = make([]byte, 16)
	lengthIV       = []byte("0000000000000000")
	lengthPassword = "JuGaiT"

	//headerMethod    = "aes-256-ctr"
	//headerPassword  = "rzx!@!*1218"
	bodyMethodsList = [...]string{
		//'aes-128-cbc','aes-192-cbc','aes-256-cbc',
		//"aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
		"aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
		//'aes-128-ofb','aes-192-ofb','aes-256-ofb',
		"aes-128-ctr", "aes-192-ctr", "aes-256-ctr",
		//'aes-128-cfb8','aes-192-cfb8','aes-256-cfb8',
		//'aes-128-cfb1','aes-192-cfb1','aes-256-cfb1','bf-cfb',
		//'camellia-128-cfb','camellia-192-cfb','camellia-256-cfb',
		//'rc4','rc4-md5','rc4-md5-6'
	}
)

type message struct {
	Time     uint32 `json:"time"`
	IV       string `json:"iv"`
	Method   string `json:"method"`
	Password string `json:"password"`
	Padding  string `json:"padding"`
}

func RandomBytesGenerator(min, max int) []byte {
	const template = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"
	length := min + math_rand.Intn(max+1-min) - 1 //[0, n)

	ret := make([]byte, length)
	for i := 0; i < length; i++ {
		ret[i] = template[math_rand.Intn(len(template))]
	}
	return ret
}
func RandomStringGenerator(min, max int) string {
	return string(RandomBytesGenerator(min, max))
}

type bytesAsConn struct {
	net.Conn
	r *bytes.Reader
}

func newBytesAsConn(c net.Conn, buf []byte) *bytesAsConn {
	return &bytesAsConn{Conn: c, r: bytes.NewReader(buf)}
}

func (conn *bytesAsConn) Read(b []byte) (int, error) {
	return conn.r.Read(b)
}

func (conn *bytesAsConn) WriteTo(w io.Writer) (int64, error) {
	return conn.r.WriteTo(w)
}

type clientConn struct {
	N.ExtendedConn
	method      *Method
	destination M.Socksaddr
	readStream  cipher.Stream
	writeStream cipher.Stream
	buf         [MAX_HEADER_AND_IV_SIZE]byte
}

func (c *clientConn) readResponse() error {
	//length 读取header+iv长度
	c.method.option.Key = nil
	c.method.option.Password = lengthPassword
	methodLength, err := NewMethod_L(c.method.ctx, lengthMethod, c.method.option)
	if err != nil {
		log.Errorln("readResponse methodLength NewMethod_L fail : %s", err.Error())
	}
	c.readStream, err = methodLength.decryptConstructor(methodLength.key, lengthIV)
	if _, err := io.ReadFull(c.ExtendedConn, c.buf[:1]); err != nil {
		log.Errorln("readResponse length error : %s", err.Error())
		return err
	}
	c.readStream.XORKeyStream(c.buf[:1], c.buf[:1])
	length := uint8(c.buf[0])
	if length == 0 {
		log.Errorln("readResponse length is zero")
		return fmt.Errorf("length is 0")
	}

	//读取header + iv
	if _, err := io.ReadFull(c.ExtendedConn, c.buf[:length]); err != nil {
		return err
	}
	ivSize := c.method.saltLength
	//GetIVSize(c.headerMethod)
	if ivSize == 0 {
		log.Errorln("readResponse ivSize is zero")
		return fmt.Errorf("ivSize is 0")
	}
	//分别获取header 和 iv
	header := c.buf[0 : length-uint8(ivSize)]
	iv := c.buf[length-uint8(ivSize) : length]
	//设置 header的编解码参数
	c.method.option.Key = nil
	c.method.option.Password = c.method.password
	headerMethod, err := NewMethod_L(c.method.ctx, c.method.methodName, c.method.option)
	if err != nil {
		log.Errorln("readResponse headerMethod NewMethod_L fail : %s", err.Error())
	}
	c.readStream, err = headerMethod.decryptConstructor(headerMethod.key, iv)
	if err != nil {
		log.Errorln("readResponse headerMethod.decryptConstructor fail : %s", err.Error())
	}
	//获取头解析器
	c.readStream.XORKeyStream(header, header)
	//message
	//log.Debugln("readResponse Receive : %d, %v, %v", length, header, iv)
	var msg message
	err = json.Unmarshal(header, &msg)
	if err != nil {
		log.Errorln("readResponse json.Unmarshal fail : %s", err.Error())
		return err
	}
	//log.Debugln("readResponse msg : %v", msg)

	//body
	iv, err = hex.DecodeString(msg.IV)
	if err != nil {
		log.Errorln("readResponse hex DecodeString IV fail : %s", err.Error())
		return err
	}
	password, err := hex.DecodeString(msg.Password)
	if err != nil {
		log.Errorln("readResponse hex DecodeString Password fail : %s", err.Error())
		return err
	}
	//设置 body 的编解码参数
	c.method.option.Key = nil
	c.method.option.Password = string(password)
	methodBody, err := NewMethod_L(c.method.ctx, msg.Method, c.method.option)
	if err != nil {
		log.Errorln("readResponse methodBody.NewMethod_L fail : %s", err.Error())
	}
	//获取body解析器
	c.readStream, err = methodBody.decryptConstructor(methodBody.key, iv)
	if err != nil {
		log.Errorln("readResponse methodBody.decryptConstructor fail : %s", err.Error())
	}
	return err
}

func (c *clientConn) Read(p []byte) (n int, err error) {
	if c.readStream == nil {
		err = c.readResponse()
		if err != nil {
			return
		}
	}
	n, err = c.ExtendedConn.Read(p)
	if err != nil {
		return
	}
	c.readStream.XORKeyStream(p[:n], p[:n])
	return
}

func (c *clientConn) WriteHeader() error {
	//body 的加密方式及加密密钥
	bodyMethod := bodyMethodsList[math_rand.Intn(len(bodyMethodsList))]
	bodyPassword := RandomStringGenerator(8, 16)
	//设置body的编解码参数
	c.method.option.Key = nil
	c.method.option.Password = bodyPassword
	bodyMethodS, err := NewMethod_L(c.method.ctx, bodyMethod, c.method.option)
	if err != nil {
		log.Errorln("WriteHeader bodyMethodS.NewMethod_L fail : %s", err.Error())
	}
	bodyIV, err := ivGenerator(c.method.saltLength)
	if err != nil {
		log.Errorln("WriteHeader bodyIV.ivGenerator fail : %s", err.Error())
	}

	//message  组装header
	var msg message
	msg.Time = uint32(time.Now().Unix())
	msg.IV = hex.EncodeToString(bodyIV)
	msg.Method = bodyMethod
	msg.Password = hex.EncodeToString([]byte(bodyPassword))
	msg.Padding = hex.EncodeToString([]byte(RandomStringGenerator(10, 30)))
	//LogDbg("%v",msg)

	//log.Debugln("WriteHeader msg : %v", msg)
	header, err := json.Marshal(&msg)
	if err != nil {
		log.Errorln("WriteHeader json.Marshal fail : %s", err.Error())
		return err
	}

	//header 设置head的编解码器
	c.method.option.Key = nil
	c.method.option.Password = c.method.password
	headMethodS, err := NewMethod_L(c.method.ctx, c.method.methodName, c.method.option)
	if err != nil {
		log.Errorln("WriteHeader headMethodS.NewMethod_L fail : %s", err.Error())
	}
	headerIV, err := ivGenerator(c.method.saltLength)
	if err != nil {
		log.Errorln("WriteHeader headerIV.ivGenerator fail : %s", err.Error())
	}
	length := len(header) + len(headerIV)
	//log.Debugln("WriteHeader : %d, %v, %v", length, header, headerIV)
	if length >= MAX_HEADER_AND_IV_SIZE {
		return fmt.Errorf("%d is more than %d\n", length, MAX_HEADER_AND_IV_SIZE)
	}

	//length 设置编码器
	c.method.option.Key = nil
	c.method.option.Password = lengthPassword
	lenMethodS, err := NewMethod_L(c.method.ctx, lengthMethod, c.method.option)
	if err != nil {
		log.Errorln("WriteHeader lenMethodS.NewMethod_L fail : %s", err.Error())
	}
	c.writeStream, err = lenMethodS.encryptConstructor(lenMethodS.key, lengthIV)
	if err != nil {
		log.Errorln("WriteHeader lenMethodS.encryptConstructor fail : %s", err.Error())
	}
	//write to  写第一位head + iv长度
	c.buf[0] = uint8(length)
	c.writeStream.XORKeyStream(c.buf[:1], c.buf[:1])
	//log.Debugln("Write Length : %v", c.buf[0])
	c.ExtendedConn.Write(c.buf[:1])

	//获取 head解释器
	c.writeStream, err = headMethodS.encryptConstructor(headMethodS.key, headerIV)
	if err != nil {
		log.Errorln("WriteHeader headMethodS.encryptConstructor fail : %s", err.Error())
	}
	c.writeStream.XORKeyStream(header, header)
	//log.Debugln("Write header XOR : %v", header)
	c.ExtendedConn.Write(header)

	//写 iv
	c.ExtendedConn.Write(headerIV)
	//LogDbg("Send: %d, %v, %v", length, header, headerIV)
	//获取body编解码器
	c.writeStream, err = bodyMethodS.encryptConstructor(bodyMethodS.key, bodyIV)
	if err != nil {
		log.Errorln("WriteHeader bodyMethodS.encryptConstructor fail : %s", err.Error())
	}
	return nil
}

func (c *clientConn) Write(p []byte) (n int, err error) {
	if c.writeStream == nil {
		c.WriteHeader()
		addrLen := M.SocksaddrSerializer.AddrPortLen(c.destination)
		buffer := buf.NewSize(addrLen)
		err = M.SocksaddrSerializer.WriteAddrPort(buffer, c.destination)
		if err != nil {
			return
		}
		c.writeStream.XORKeyStream(buffer.To(addrLen), buffer.To(addrLen))
		c.ExtendedConn.Write(buffer.Bytes())
	}
	c.writeStream.XORKeyStream(p, p)
	return c.ExtendedConn.Write(p)
}

func (c *clientConn) ReadBuffer(buffer *buf.Buffer) error {
	if c.readStream == nil {
		err := c.readResponse()
		if err != nil {
			return err
		}
	}

	err := c.ExtendedConn.ReadBuffer(buffer)
	if err != nil {
		return err
	}
	c.readStream.XORKeyStream(buffer.Bytes(), buffer.Bytes())
	return nil
}

func (c *clientConn) WriteBuffer(buffer *buf.Buffer) error {
	if c.writeStream == nil {
		c.WriteHeader()
		addrLen := M.SocksaddrSerializer.AddrPortLen(c.destination)
		buffer := buf.NewSize(addrLen)
		err := M.SocksaddrSerializer.WriteAddrPort(buffer, c.destination)
		if err != nil {
			return err
		}
		c.writeStream.XORKeyStream(buffer.To(addrLen), buffer.To(addrLen))
		c.ExtendedConn.Write(buffer.Bytes())
	}
	c.writeStream.XORKeyStream(buffer.Bytes(), buffer.Bytes())
	return c.ExtendedConn.WriteBuffer(buffer)
}

func (c *clientConn) FrontHeadroom() int {
	if c.writeStream == nil {
		return c.method.saltLength + M.SocksaddrSerializer.AddrPortLen(c.destination)
	}
	return 0
}

func (c *clientConn) NeedHandshake() bool {
	return c.writeStream == nil
}

func (c *clientConn) Upstream() any {
	return c.ExtendedConn
}

type clientPacketConn struct {
	N.ExtendedConn
	method *Method
}

func (c *clientPacketConn) ReadPacket(buffer *buf.Buffer) (destination M.Socksaddr, err error) {
	err = c.ReadBuffer(buffer)
	if err != nil {
		return
	}
	stream, err := c.method.decryptConstructor(c.method.key, buffer.To(c.method.saltLength))
	if err != nil {
		return
	}
	stream.XORKeyStream(buffer.From(c.method.saltLength), buffer.From(c.method.saltLength))
	buffer.Advance(c.method.saltLength)
	destination, err = M.SocksaddrSerializer.ReadAddrPort(buffer)
	if err != nil {
		return
	}
	return destination.Unwrap(), nil
}

func (c *clientPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	header := buf.With(buffer.ExtendHeader(c.method.saltLength + M.SocksaddrSerializer.AddrPortLen(destination)))
	header.WriteRandom(c.method.saltLength)
	err := M.SocksaddrSerializer.WriteAddrPort(header, destination)
	if err != nil {
		return err
	}
	stream, err := c.method.encryptConstructor(c.method.key, buffer.To(c.method.saltLength))
	if err != nil {
		return err
	}
	stream.XORKeyStream(buffer.From(c.method.saltLength), buffer.From(c.method.saltLength))
	return c.ExtendedConn.WriteBuffer(buffer)
}

func (c *clientPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, err = c.ExtendedConn.Read(p)
	if err != nil {
		return
	}
	stream, err := c.method.decryptConstructor(c.method.key, p[:c.method.saltLength])
	if err != nil {
		return
	}
	buffer := buf.As(p[c.method.saltLength:n])
	stream.XORKeyStream(buffer.Bytes(), buffer.Bytes())
	destination, err := M.SocksaddrSerializer.ReadAddrPort(buffer)
	if err != nil {
		return
	}
	if destination.IsFqdn() {
		addr = destination
	} else {
		addr = destination.UDPAddr()
	}
	n = copy(p, buffer.Bytes())
	return
}

func (c *clientPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	destination := M.SocksaddrFromNet(addr)
	buffer := buf.NewSize(c.method.saltLength + M.SocksaddrSerializer.AddrPortLen(destination) + len(p))
	defer buffer.Release()
	buffer.WriteRandom(c.method.saltLength)
	err = M.SocksaddrSerializer.WriteAddrPort(buffer, destination)
	if err != nil {
		return
	}
	stream, err := c.method.encryptConstructor(c.method.key, buffer.To(c.method.saltLength))
	if err != nil {
		return
	}
	stream.XORKeyStream(buffer.From(c.method.saltLength), buffer.From(c.method.saltLength))
	stream.XORKeyStream(buffer.Extend(len(p)), p)
	_, err = c.ExtendedConn.Write(buffer.Bytes())
	if err == nil {
		n = len(p)
	}
	return
}

func (c *clientPacketConn) FrontHeadroom() int {
	return c.method.saltLength + M.MaxSocksaddrLength
}

func (c *clientPacketConn) Upstream() any {
	return c.ExtendedConn
}
