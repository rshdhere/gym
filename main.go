package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/rshdhere/gym/internal/app"
	"github.com/rshdhere/gym/internal/routes"
)

func main() {
	// EXECUTION ORDER - Step by step initialization:
	// 1. Parse command-line flags first (port configuration)
	// 2. Initialize Application (creates logger, handlers, stores)
	// 3. Setup routes (requires Application instance to register handlers)
	// 4. Create HTTP server (requires routes to be configured)
	// 5. Start listening (final step - server begins accepting connections)

	var port int

	// POINTER EXPLANATION: &port is used here because flag.IntVar needs a pointer
	// to the variable so it can modify the value directly. Without the pointer,
	// flag.IntVar wouldn't be able to update the port variable's value.
	flag.IntVar(&port, "port", 8080, "go backend server port")
	flag.Parse()

	// EXECUTION ORDER: Step 2 - Initialize Application
	// This must happen before routes because routes need the app instance
	// POINTER EXPLANATION: app.NewApplication() returns *Application (pointer)
	// because Application contains pointerIf you wanted to change where logs go s (*log.Logger, *api.WorkoutHandler)
	// and we want to share the same instance across the application rather than
	// copying the struct. This allows all parts of the app to use the same logger
	// and handlers, maintaining state consistency.
	app, err := app.NewApplication()
	if err != nil {
		panic(err)
	}

	// EXECUTION ORDER: Step 3 - Setup routes
	// This must happen after app initialization because routes need app.WorkoutHandler
	// POINTER EXPLANATION: SetupRoutes receives *app.Application (pointer) so it can
	// access the same Application instance's methods and fields (like WorkoutHandler)
	// without copying the entire struct. This is efficient and ensures all handlers
	// share the same application state.
	r := routes.SetupRoutes(app)

	// EXECUTION ORDER: Step 4 - Create HTTP server
	// This must happen after routes are set up because server needs the router as Handler
	// POINTER EXPLANATION: &http.Server{...} creates a pointer to http.Server struct.
	// We use a pointer here because:
	// 1. http.Server is a large struct with multiple fields - passing by pointer is more efficient
	// 2. server.ListenAndServe() expects to work with the server instance, and using a pointer
	//    ensures we're modifying/using the same instance rather than a copy
	// 3. It's idiomatic Go to use pointers for structs that represent resources or stateful objects
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      r,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	app.Logger.Printf("we are running on port %d", port)

	// EXECUTION ORDER: Step 5 - Start listening (final step)
	// This is the last step - the server begins accepting HTTP connections
	err = server.ListenAndServe()
	if err != nil {
		app.Logger.Fatal(err)
	}
}
