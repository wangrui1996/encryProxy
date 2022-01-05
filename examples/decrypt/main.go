package main

import (
	"log"
	"portForward/proxy"
)

func main() {
    str := "bFVYVAVAU2Jt35mEX0LC5zCML5-szPPNUJBS44nTxdePFtiQ_9S81kCUTJNH5CBeYdsu-098tkl6v0uVLcfLSm4EuReVfmP8t90NnEPaA8auuw-6vtHonpduvu0gCgf30yy9nnhFqur94WVEOesed3hC4uyQQg9HVOIX-YNbXjY45bcGVks-YtMVaoWPe1I7"
	bkey := "LKHlhb899Y09olUi"
	key := []byte(bkey)

	msg, _ := proxy.Decrypt(key, str)
	log.Println(msg)
}
