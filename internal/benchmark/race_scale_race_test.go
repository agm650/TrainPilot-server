//go:build race

package benchmark_test

// raceTimeScale élargit les fenêtres de temps du test d'intégration
// quand le binaire est compilé avec -race, pour compenser le
// ralentissement introduit par l'instrumentation du race detector.
const raceTimeScale = 4
