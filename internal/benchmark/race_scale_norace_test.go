//go:build !race

package benchmark_test

// raceTimeScale reste à 1 en exécution normale (sans -race).
const raceTimeScale = 1
