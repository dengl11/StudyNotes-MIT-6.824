package mapreduce

import (
    "fmt"
    "log"
)
//
// schedule() starts and waits for all tasks in the given phase (Map
// or Reduce). the mapFiles argument holds the names of the files that
// are the inputs to the map phase, one per map task. nReduce is the
// number of reduce tasks. the registerChan argument yields a stream
// of registered workers; each item is the worker's RPC address,
// suitable for passing to call(). registerChan will yield all
// existing registered workers (if any) and new ones as they register.
//
func schedule(jobName string,
              mapFiles []string,
              nReduce int,
              phase jobPhase,
              registerChan chan string) {
	var ntasks int
	var n_other int // number of inputs (for reduce) or outputs (for map)
    switch phase {
    case mapPhase:
        ntasks = len(mapFiles)
        n_other = nReduce
    case reducePhase:
        ntasks = nReduce
        n_other = len(mapFiles)
	}

	fmt.Printf("Schedule: %v %v tasks (%d I/Os)\n", ntasks, phase, n_other)

	// All ntasks tasks have to be scheduled on workers, and only once all of
	// them have been completed successfully should the function return.
	// Remember that workers may fail, and that any given worker may finish
	// multiple tasks.
	//
    // TODO
    //
    todo := make([]int, ntasks) // an array to keep track of jobs to do
    for i := 0; i < ntasks; i++ {
       todo[i] = i
    }
    nDone := 0
    for ; ; {
        if nDone >= ntasks {
            break
        }
        // First wait for an available worker
        log.Printf("Scheduler waiting for worker\n")
        workerAddr := <-registerChan // get an available worker
        log.Printf("Scheduler get a new worker: %v\n", workerAddr)

        // If all jobs are being done, continue to the next loop for an available worker
        if len(todo) == 0 {
            continue
        }
        var job int
        job, todo = todo[0], todo[1:]
        //log.Printf("Scheduler running job: %v\n", job)
        var file string
        if phase == mapPhase {
            file = mapFiles[job]
        }
        args := DoTaskArgs{jobName, file, phase, job, n_other}
        go func(i int) {
            success := call(workerAddr, "Worker.DoTask", args, nil)
            if !success {
                log.Printf("RPC failed on worker: %v\n", workerAddr)
                todo = append(todo, i) // Add back the failed job
            } else {
                nDone += 1
                registerChan <- workerAddr // Re-use the worker who has just completed its job
            }
        }(job)
    }
    fmt.Printf("Schedule: %v phase done\n", phase)
}
