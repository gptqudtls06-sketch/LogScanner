package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
)

func main() {
	// 1. CLI 플래그 정의
	path := flag.String("path", "./logs/*.log", "로그 파일 경로(와일드카드 지원)")
	flag.Parse()

	// 2. 파일 찾기
	files, err := filepath.Glob(*path)
	if err != nil {
		log.Fatal(err)
	}

	// 3. 결과 출력
	fmt.Println("찾은 로그 파일:")
	for _, f := range files {
		fmt.Println("-", f)
	}

	if len(files) == 0 {
		fmt.Println("로그 파일을 찾지 못했습니다.")
	}
}
