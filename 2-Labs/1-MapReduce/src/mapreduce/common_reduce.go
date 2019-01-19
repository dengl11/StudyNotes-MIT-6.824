package mapreduce

import (
    "log"
    "os"
    "sort"
    "encoding/json"
)


// doReduce manages one reduce task: it reads the intermediate
// key/value pairs (produced by the map phase) for this task, sorts the
// intermediate key/value pairs by key, calls the user-defined reduce function
// (reduceF) for each key, and writes the output to disk.
func doReduce(
	jobName string, // the name of the whole MapReduce job
	reduceTaskNumber int, // which reduce task this is
	outFile string, // write the output here
	nMap int, // the number of map tasks that were run ("M" in the paper)
	reduceF func(key string, values []string) string,
) {
	//
	// You will need to write this function.
	//
	// You'll need to read one intermediate file from each map task;
	// reduceName(jobName, m, reduceTaskNumber) yields the file
	// name from map task m.
	//
	// Your doMap() encoded the key/value pairs in the intermediate
	// files, so you will need to decode them. If you used JSON, you can
	// read and decode by creating a decoder and repeatedly calling
	// .Decode(&kv) on it until it returns an error.
	//
	// You may find the first example in the golang sort package
	// documentation useful.
	//
	// reduceF() is the application's reduce function. You should
	// call it once per distinct key, with a slice of all the values
	// for that key. reduceF() returns the reduced value for that key.
	//
	// You should write the reduce output as JSON encoded KeyValue
	// objects to the file named outFile. We require you to use JSON
	// because that is what the merger than combines the output
	// from all the reduce tasks expects. There is nothing special about
	// JSON -- it is just the marshalling format we chose to use. Your
	// output code will look something like this:
	//
	// enc := json.NewEncoder(file)
	// for key := ... {
	// 	enc.Encode(KeyValue{key, reduceF(...)})
	// }
	// file.Close()
	//
    allKVs := make([]KeyValue, 0, 100)
    // 1) prepare intermediate file names that this reducer needs
    for i := 0; i < nMap; i++ {
        name := reduceName(jobName, i, reduceTaskNumber)
        file, err := os.Open(name)
        if err != nil {
            log.Fatalf("Error in reducer: %v\n", err)
        }
        dec := json.NewDecoder(file)
        var kv KeyValue
        for {
            if err := dec.Decode(&kv); err == nil {
                allKVs = append(allKVs, kv)
            } else {
                break
            }
        }
    }
    // 2) Sort the pairs
    sort.Slice(allKVs, func(i, j int) bool {
        return allKVs[i].Key < allKVs[j].Key
    })

    // 3) Segment them by key
    data := make([]KeyValue, 0, 100)
    curr := make([]string, 0, 100)
    var preKey string
    for i, kv := range allKVs {
        if i > 0 && preKey != kv.Key {
            reduced := reduceF(preKey, curr)
            data = append(data, KeyValue{preKey, reduced})
            curr = make([]string, 100)
        }
        curr = append(curr, kv.Value)
        preKey = kv.Key
    }
    if len(curr) > 0 {
        data = append(data, KeyValue{preKey, reduceF(preKey, curr)})
    }
    // 4) Write them to outFile
    f, _ := os.Create(outFile)
    enc := json.NewEncoder(f)
    for _, kv := range data {
        err := enc.Encode(kv)
        if err != nil {
            log.Fatalf("Error in reducer encoding: %v\n", err)
        }
    }
    f.Close()
}
