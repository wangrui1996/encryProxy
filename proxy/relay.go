
package proxy

import (
	"io"
)

func copyBuffer(dst io.Writer, src io.Reader, buf []byte, decoder Codec, encoder Codec) (written int64, err error) {
	if buf == nil {
		buf = make([]byte, framePayloadSize)
	}

	for {
		// 读取并解码
		plaintext, readErr := decoder.Decode(src, buf)

		// 有数据就先编码，再写入目标。
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
