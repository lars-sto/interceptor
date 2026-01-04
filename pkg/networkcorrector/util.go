package networkcorrector

import "math/rand"

func randomDrop(dropProbability float64) bool {
	return rand.Float64() >= dropProbability
}
