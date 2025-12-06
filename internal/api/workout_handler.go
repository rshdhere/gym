package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type WorkoutHandler struct{}

// EXECUTION ORDER: This function is called in Step 2.2 of app.go (during Application initialization)
// It creates the WorkoutHandler instance that will be used by routes.
// POINTER EXPLANATION: Returns *WorkoutHandler (pointer) because:
//  1. Handler structs are typically shared instances - using a pointer ensures all parts
//     of the application use the same handler instance
//  2. Methods use pointer receivers (wh *WorkoutHandler) - returning a pointer is consistent
//  3. It's more efficient - avoids copying the struct when passing it around
//  4. If the handler needs to maintain state in the future, using a pointer allows that
func NewWorkoutHandler() *WorkoutHandler {
	return &WorkoutHandler{}
}

// POINTER EXPLANATION: (wh *WorkoutHandler) is a pointer receiver. We use a pointer receiver because:
// 1. It's idiomatic Go for handler methods - handlers are typically shared instances
// 2. If we ever need to add state to WorkoutHandler (like a store reference), we can modify it
// 3. It's consistent with how handlers are passed around (as pointers)
// 4. More efficient - avoids copying the struct when the method is called
// POINTER EXPLANATION: w http.ResponseWriter and r *http.Request - Request is a pointer because
// it's a large struct with many fields. ResponseWriter is an interface, so it's already a reference type.
func (wh *WorkoutHandler) HandleGetWorkoutById(w http.ResponseWriter, r *http.Request) {
	paramsWorkoutID := chi.URLParam(r, "id")

	if paramsWorkoutID == "" {
		http.NotFound(w, r)
		return
	}

	workoutId, err := strconv.ParseInt(paramsWorkoutID, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	fmt.Fprintf(w, "this is the workout id %d\n", workoutId)
}

// POINTER EXPLANATION: (wh *WorkoutHandler) - same reasoning as HandleGetWorkoutById above
// Pointer receiver for consistency and efficiency
func (wh *WorkoutHandler) HandleCreateWorkout(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "created a workout\n")
}
