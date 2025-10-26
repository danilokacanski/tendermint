package main

import "sync"

func RunConsensus(nodes []*Node, height int, startRound int) bool {
	var wg sync.WaitGroup
	results := make(chan bool, len(nodes))

	for _, n := range nodes {
		wg.Add(1)
		go func(node *Node) {
			defer wg.Done()
			results <- node.StartConsensus(height, startRound, len(nodes))
		}(n)
	}

	wg.Wait()
	close(results)

	success := true
	for r := range results {
		if !r {
			success = false
		}
	}
	return success
}
