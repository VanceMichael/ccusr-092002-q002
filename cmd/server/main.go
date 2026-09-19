package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/httpapi"
	"github.com/vancemichael/092002-industrial-visit-intent/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, os.Getenv("DATABASE_PATH"))
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer db.Close()

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           httpapi.Router(db),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("合作线索归口服务启动，监听 :%s", port)
	log.Fatal(server.ListenAndServe())
}
