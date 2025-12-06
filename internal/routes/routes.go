package routes

import (
	"github.com/go-chi/chi/v5"
	"github.com/rshdhere/gym/internal/app"
)

// EXECUTION ORDER: This function is called in Step 3 of main.go
// It sets up routes in this order:
// 1. Create router instance (chi.NewRouter)
// 2. Register GET routes (health check, get workout by ID)
// 3. Register POST routes (create workout)
// 4. Return configured router
// POINTER EXPLANATION: app *app.Application is a pointer parameter because:
//  1. Application struct contains pointers (*log.Logger, *api.WorkoutHandler) - we want to
//     access the same instance, not a copy
//  2. It's more efficient - avoids copying the Application struct
//  3. Allows us to call methods on app (like app.HealthCheck) which use pointer receivers
//
// POINTER EXPLANATION: Returns *chi.Mux (pointer) because chi.NewRouter() returns a pointer.
// The router is a stateful object that maintains route registrations, so using a pointer
// ensures we're working with the same router instance throughout the application.
func SetupRoutes(app *app.Application) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/health", app.HealthCheck)
	r.Get("/workouts/{id}", app.WorkoutHandler.HandleGetWorkoutById)

	r.Post("/workouts", app.WorkoutHandler.HandleCreateWorkout)
	return r
}
