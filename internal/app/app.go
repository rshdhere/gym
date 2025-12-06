package app

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/rshdhere/gym/internal/api"
)

type Application struct {
	// POINTER EXPLANATION: Logger is *log.Logger (pointer) because log.Logger is an interface
	// type that contains internal state. Using a pointer ensures we're working with the same
	// logger instance across the application, allowing consistent logging behavior.
	Logger *log.Logger
	// POINTER EXPLANATION: WorkoutHandler is *api.WorkoutHandler (pointer) because:
	// 1. Handler structs typically contain state or are meant to be shared instances
	// 2. Using a pointer avoids copying the struct when passing it around
	// 3. It's idiomatic Go to use pointers for handler/service structs
	WorkoutHandler *api.WorkoutHandler
}

// EXECUTION ORDER: This function is called in Step 2 of main.go
// It initializes all application dependencies in this order:
// 1. Create logger (needed for logging throughout the app)
// 2. Initialize store (will be added here - needed by handlers)
// 3. Initialize handlers (need store to be ready)
// 4. Create Application struct with all dependencies
func NewApplication() (*Application, error) {
	// POINTER EXPLANATION: log.New returns *log.Logger (pointer) because log.Logger
	// is a struct that contains internal state (like output destination, flags, etc.)
	// and we want to share the same logger instance across the application.
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime)

	// EXECUTION ORDER: Step 2.1 - Store initialization
	// This should be created first because handlers will depend on it
	// our store will go in here

	// EXECUTION ORDER: Step 2.2 - Handler initialization
	// This should be created after store because handlers need store to operate
	// our handlers will go in here

	// POINTER EXPLANATION: api.NewWorkoutHandler() returns *WorkoutHandler (pointer)
	// because handlers are typically shared instances that contain methods (not just data).
	// Using a pointer is more efficient and allows the handler to maintain state if needed.
	workoutHandler := api.NewWorkoutHandler()

	// POINTER EXPLANATION: &Application{...} creates a pointer to Application struct.
	// We return a pointer because:
	// 1. Application contains pointers (*log.Logger, *api.WorkoutHandler) - we want to share
	//    the same instance, not copy it
	// 2. Application is passed around to routes and handlers - using a pointer avoids
	//    copying the entire struct (more efficient)
	// 3. It allows methods like HealthCheck to use pointer receivers, enabling them to
	//    modify the Application if needed (though not used here, it's good practice)
	app := &Application{
		Logger:         logger,
		WorkoutHandler: workoutHandler,
	}

	return app, nil
}

// POINTER EXPLANATION: (a *Application) is a pointer receiver. We use a pointer receiver because:
// 1. Application contains pointers to other structs - using a pointer receiver avoids copying
// 2. If we ever need to modify Application state in the method, we can do so
// 3. It's consistent with the fact that Application is passed around as a pointer
// 4. It's more efficient - avoids copying the Application struct when calling the method
func (a *Application) HealthCheck(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "status is available")
}
