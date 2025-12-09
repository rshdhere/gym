package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/rshdhere/gym/internal/app"
	"github.com/rshdhere/gym/internal/routes"
)

func main() {

	var port int

	flag.IntVar(&port, "port", 8080, "go backend server port")
	flag.Parse()
	ctx := context.Background()
	app, err := app.NewApplication(ctx)
	if err != nil {
		panic(err)
	}

	defer app.DB.Close()

	r := routes.SetupRoutes(app)
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      r,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	app.Logger.Printf("we are running on port %d", port)

	err = server.ListenAndServe()
	if err != nil {
		app.Logger.Fatal(err)
	}
}
