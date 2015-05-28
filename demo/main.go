package main

import (
	"fmt"
	"os"

	"github.com/holys/baidu-pcs"
)

func main() {
	token := os.Getenv("BAIDU_PCS_TOKEN")
	if token == "" {
		panic("token not found")
	}
	client := pcs.NewClient(token)
	fmt.Println(client.baseURL)

}
