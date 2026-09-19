
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/httpapi"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	log.Fatal(http.ListenAndServe(":"+port, httpapi.Router()))
}
