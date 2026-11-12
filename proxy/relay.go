package proxy

import "io"

func copyBuffer(dst io.Writer, src io.Reader, buf []byte, decoder Codec, encoder Codec) (written int64, err error) {
    if buf == nil {
        buf = make([]byte, framePayloadSize)
    }

    for {
        plaintext, readErr := decoder.Decode(src, buf)
        if len(plaintext) > 0 {
            n, writeErr := encoder.Encode(dst, plaintext)
            written += int64(n)
            if writeErr != nil {
                return written, writeErr
            }
        }

        if readErr != nil {
            if readErr == io.EOF {
                return written, nil
            }
            return written, readErr
        }
    }
}

// 下游 -> 上游：按 Inbound 解码，再按 Outbound 编码。
func (p *Proxy) handleUpstream(srcConn, dstConn io.ReadWriter, outbound *OutboundConfig) error {
    decoder, err := NewCodec(p.inbound.Crypto.Enabled, p.inbound.Crypto.Key)
    if err != nil {
        return err
    }
    encoder, err := NewCodec(outbound.Crypto.Enabled, outbound.Crypto.Key)
    if err != nil {
        return err
    }
    _, err = copyBuffer(dstConn, srcConn, nil, decoder, encoder)
    return err
}

// 上游 -> 下游：按 Outbound 解码，再按 Inbound 编码。
func (p *Proxy) handleDownstream(srcConn, dstConn io.ReadWriter, outbound *OutboundConfig) error {
    decoder, err := NewCodec(outbound.Crypto.Enabled, outbound.Crypto.Key)
    if err != nil {
        return err
    }
    encoder, err := NewCodec(p.inbound.Crypto.Enabled, p.inbound.Crypto.Key)
    if err != nil {
        return err
    }
    _, err = copyBuffer(dstConn, srcConn, nil, decoder, encoder)
    return err
}
