package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
)

func main() {

	idsSlice := []string{}
	for i := 0; i < 50; i++ {
		idsSlice = append(idsSlice, strconv.Itoa(i))
	}

	ids := strings.Join(idsSlice, ",")
	url := "https://wondernetwork.com/ping-data?sources=" + ids + "&destinations=" + ids

	req, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	defer req.Body.Close()

	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println("- data -")
	fmt.Println(string(data))
}
