package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import "sync"
import "labrpc"
import "time"

//import "fmt"
import "math/rand"

import "bytes"
import "encoding/gob"

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make().
//
type ApplyMsg struct {
	Index       int
	Command     interface{}
	UseSnapshot bool   // ignore for lab2; only used in lab3
	Snapshot    []byte // ignore for lab2; only used in lab3
}

type AppendEntriesArgs struct {
	Leader          int        // Leader index
	Term            int        // Leader Term
	PrevLogIndex    int        // index of the log entry immediately preceeding new ones
	PrevLogTerm     int        // term of the log entry immediately preceeding new ones
	Entries         []LogEntry // log entries to store
	LeaderCommitIdx int        // leader's commitIndex
}

type LogEntry struct {
	Command interface{}
	Term    int
}

type AppendEntriesReply struct {
	Term    int  // Server's currentTerm
	Success bool // true if appendEntries succeeds
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu                  sync.Mutex          // Lock to protect shared access to this peer's state
	requestingVoteMutex sync.Mutex          // Lock to protect shared access to this peer's state
	peers               []*labrpc.ClientEnd // RPC end points of all peers
	persister           *Persister          // Object to hold this peer's persisted state
	me                  int                 // this peer's index into peers[]
	heartbeat           chan bool           // Channel for receiving heartbeat
	isLeader            bool
	synched             bool
	applyCh             chan ApplyMsg

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// Persistent states
	currentTerm int
	votedFor    int
	logs        []LogEntry
	// Volatile states
	commitIndex int // Index of highest log entry known to be committed
	lastApplied int // Index of highest log entry applied to state machine

	// Leader states
	nextIndexes  []int // Next log index to send for each server
	matchIndexes []int // Highest log index replicated on each server
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).
	//DPrintf("Loking on getting state for server %d", rf.me)
	rf.mu.Lock()
	//DPrintf("Acquired lock on getting state for server %d", rf.me)
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.isLeader
}

// Check isLeader in thread-safe way
func (rf *Raft) getIsLeader() bool {
	//DPrintf("Loking on get isLeader")
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.isLeader
}

// Check me in thread-safe way
func (rf *Raft) getMe() int {
	//DPrintf("Loking on get isLeader")
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.me
}

// Check isLeader in thread-safe way
func (rf *Raft) getCurrentTerm() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm
}

// Check isLeader in thread-safe way
func (rf *Raft) setLeader(isLeader bool) {
	//DPrintf("Loking on set isLeader")
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.isLeader = isLeader
	if isLeader {
		DPrintf("\n ----- NEW LEADER: %d - Term: %v (C = %v, logs = %v, nextIdx = %v) -----\n", rf.me, rf.currentTerm, rf.commitIndex, rf.logs, rf.nextIndexes)
	}
}

func (rf *Raft) setCurrentTerm(term int) {
	if term != rf.currentTerm {
		//DPrintf("Set server %v currentTerm: %v -> %v", rf.me, rf.currentTerm, term)
		rf.currentTerm = term
	}
}

// Return RPC-ok, reply-success
func (rf *Raft) sendAppendEntriesToServer(server int, empty bool) (bool, bool) {
	var appendEntriesArgs AppendEntriesArgs
	var appendEntriesReply AppendEntriesReply

	rf.mu.Lock()
	// ----------------------------------------

	//sendToMyself := server == rf.me
	appendEntriesArgs.Leader = rf.me
	appendEntriesArgs.Term = rf.currentTerm
	prevIndex := rf.nextIndexes[server] - 1
	appendEntriesArgs.PrevLogIndex = prevIndex
	nLogs := len(rf.logs)
	//DPrintf("prevIndex =  %d", prevIndex)
	if prevIndex >= 0 {
		appendEntriesArgs.PrevLogTerm = rf.logs[prevIndex].Term
	}
	if !empty {
		appendEntriesArgs.Entries = rf.logs[prevIndex+1:]
		DPrintf("leader %v send data to server %v , prevIndex = %v, entries = %v", rf.me, server, prevIndex, appendEntriesArgs.Entries)
	}

	appendEntriesArgs.LeaderCommitIdx = rf.commitIndex

	// ----------------------------------------
	rf.mu.Unlock()

	//if !empty && nLogs <= prevIndex+1 { // No need to send data
	//return true, true
	//}

	callSuccess := rf.peers[server].Call("Raft.AppendEntries", &appendEntriesArgs, &appendEntriesReply)

	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf("After Leader %v send to server %v:  callSuccess = %v, appendEntriesReply.Success = %v, appendEntriesReply.Term = %v, rf.currentTerm = %v, Empty = %v", rf.me, server, callSuccess, appendEntriesReply.Success, appendEntriesReply.Term, rf.currentTerm, empty)
	if callSuccess && !appendEntriesReply.Success && appendEntriesReply.Term > rf.currentTerm {
		rf.isLeader = false
		DPrintf("Leader %v Step down ....", rf.me)
		return callSuccess, appendEntriesReply.Success
	}
	if callSuccess {
		rf.updateWithAppendEntriesReply(server, &appendEntriesReply, nLogs, empty)
	}
	return callSuccess, appendEntriesReply.Success
}

func (rf *Raft) updateWithAppendEntriesReply(idx int, appendEntriesReply *AppendEntriesReply, newNextIdx int, empty bool) {
	if !appendEntriesReply.Success {
		if rf.nextIndexes[idx] > rf.matchIndexes[idx]+1 {
			rf.nextIndexes[idx]--
			DPrintf("Fail: Updated Leader %v matchIndexes: %v, nextIndexes = %v", rf.me, rf.matchIndexes, rf.nextIndexes)
		}
	} else if !empty {
		rf.nextIndexes[idx] = newNextIdx
		rf.matchIndexes[idx] = newNextIdx - 1
		DPrintf("Success: Updated Leader %v matchIndexes: %v, nextIndexes = %v", rf.me, rf.matchIndexes, rf.nextIndexes)
	}
}

func (rf *Raft) keepSendAppendEntriesToServer(idx int, empty bool) bool {
	for { // Keep sendAppendEntriesToServer until accepted or lost connection
		//DPrintf("sendAppendEntriesToServer: leader %v send data to server %v, empty: %v |", rf.me, idx, empty)
		if !rf.getIsLeader() {
			return false
		}
		var ok, accepted bool
		ok, accepted = rf.sendAppendEntriesToServer(idx, empty)
		//DPrintf("---> sendAppendEntriesToServer: leader %v send data to server %v, empty: %v | accepted = %v", rf.me, idx, empty, accepted)

		//if !ok {
		//return false
		//}
		if ok {
			return accepted
		}
		if empty {
			return false
		}

		// Keep looping if try to send data but got rejected
	}
}

func (rf *Raft) sendAppendEntriesEmpty() bool {
	//DPrintf("nextIndexes: %v", rf.nextIndexes)

	//var wg sync.WaitGroup

	for i := 0; i < len(rf.peers); i++ {
		//wg.Add(1)
		go func(idx int) {
			rf.sendAppendEntriesToServer(idx, true)
			//rf.keepSendAppendEntriesToServer(idx, true)
			//wg.Done()
		}(i)
	}
	//wg.Wait()
	return true
}

// Leader send heartbeats
// return true if get success from majority
func (rf *Raft) sendAppendEntries() bool {
	//DPrintf("nextIndexes: %v", rf.nextIndexes)

	waitForMajoritySuccess := make(chan bool)
	nSuccess := 0 // number of successes for AppendEntries

	for i := 0; i < len(rf.peers); i++ {
		if i == rf.getMe() {
			nSuccess++
			continue // if not heartbeat, skip leader itself
		}
		//DPrintf("Server %d send heartbeat to server %d", rf.me, i)
		go func(idx int) {
			if rf.keepSendAppendEntriesToServer(idx, false) {
				nSuccess++
			}
			if nSuccess > len(rf.peers)/2 {
				waitForMajoritySuccess <- true
			}
		}(i)
	}
	<-waitForMajoritySuccess
	return nSuccess > len(rf.peers)/2
}

// AppendEntries handler
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	DPrintf("Server %d get AppendEntries from leader %d", rf.me, args.Leader)
	DPrintf("args.Term: %v, my term: %d", args.Term, rf.currentTerm)

	commitIndexUpdated := false
	prevIndex := args.PrevLogIndex
	prevTerm := args.PrevLogTerm

	rf.mu.Lock()
	//DPrintf("Acquired lock for getting heartbeat on server %d", rf.me)
	reply.Term = rf.currentTerm

	accepted := false
	sendHeartBeat := true
	empty := args.Entries == nil

	// 1) Check term is updated enough?
	if args.Term < rf.currentTerm {
		DPrintf("😡Rejects outdated append-entries ... ")
		sendHeartBeat = false
		goto done
	}

	if args.Term > rf.currentTerm {
		rf.isLeader = false
		rf.synched = false
	}

	// 2) Check log mismatch
	if prevIndex >= 0 && (len(rf.logs) <= prevIndex || rf.logs[prevIndex].Term != prevTerm) {
		DPrintf("Server %v Rejects server %v because of mismatch: prevIndex = %v, logs = %v, prevTerm = %v\n\n", rf.me, args.Leader, prevIndex, rf.logs, prevTerm)
		rf.synched = false
		if len(rf.logs) > prevIndex {
			//fmt.Printf("rf.logs[prevIndex].Term = %v\n\n", rf.logs[prevIndex].Term)
		}
		goto done
	}
	if empty { // heartbeat
		//DPrintf("🧡")
		accepted = true
		goto done
	} else {
		accepted = rf.OnAppendEntriesData(args, reply)
	}

done:
	reply.Success = accepted
	DPrintf("Server %d (Term: %v) reply AppendEntries from leader %d (Term = %v): accepted = %v, empty = %v\n", rf.me, rf.currentTerm, args.Leader, args.Term, accepted, empty)
	if sendHeartBeat {
		rf.heartbeat <- true
	}
	if accepted {
		rf.isLeader = (args.Leader == rf.me)

		rf.setCurrentTerm(args.Term)
		rf.votedFor = -1
		rf.persist()

		DPrintf("Server: %v, Leader: %v,  LeaderCommitIdx = %v, server commit = %v | Now server logs = %v, synched = %v", rf.me, args.Leader, args.LeaderCommitIdx, rf.commitIndex, len(rf.logs), rf.synched)
		if rf.synched && args.LeaderCommitIdx > rf.commitIndex {
			commitIndexUpdated = rf.updateCommitIndex(min(args.LeaderCommitIdx, len(rf.logs)))
			if commitIndexUpdated {
				//DPrintf("💤🌕🌕Set server %v commitIndex as %v", rf.me, rf.commitIndex)
			}
		}
	}
	rf.mu.Unlock()

	if commitIndexUpdated {
		rf.sendApplyCh()
	}
}

// return accepted
func (rf *Raft) OnAppendEntriesData(args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	// Check the term at PrevLogIndex

	prevIndex := args.PrevLogIndex
	curr := prevIndex + 1
	DPrintf("OnAppendEntriesData: prevIndex = %v", prevIndex)
	firstMismatch := -1
	for i := curr; i < len(rf.logs) && (i-curr) < len(args.Entries); i++ {
		if rf.logs[i].Command != args.Entries[i-curr].Command {
			firstMismatch = i
			break
		}
	}
	if firstMismatch >= 0 {
		rf.logs = rf.logs[:firstMismatch]
		rf.logs = append(rf.logs, args.Entries[firstMismatch-curr:]...)
	} else {
		add := curr + len(args.Entries) - len(rf.logs)
		if add > 0 {
			offset := len(args.Entries) - add
			rf.logs = append(rf.logs, args.Entries[offset:]...)
		}
	}
	rf.persist()
	rf.synched = true
	DPrintf("Update server %v logs = %v", rf.me, rf.logs)

	return true
}

func (rf *Raft) updateCommitIndex(newCommit int) bool {
	if rf.commitIndex >= newCommit {
		return false
	}
	//DPrintf("Server %v update commitIndex: %v -> %v", rf.me, rf.commitIndex, newCommit)
	rf.commitIndex = newCommit
	return true
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := gob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
	w := new(bytes.Buffer)
	e := gob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.logs)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := gob.NewDecoder(r)
	// d.Decode(&rf.xxx)
	// d.Decode(&rf.yyy)
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	r := bytes.NewBuffer(data)
	d := gob.NewDecoder(r)
	d.Decode(&rf.currentTerm)
	d.Decode(&rf.votedFor)
	d.Decode(&rf.logs)
	DPrintf("[%v] readPersist: currentTerm = %v, votedFor = %v, logs = %v", rf.me, rf.currentTerm, rf.votedFor, rf.logs)
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int // candidate's term
	MyId         int // candidate Id
	LastLogIndex int // Index of the candidate's last log entry
	LastLogTerm  int // term of the candidate's last log entry
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	VoteGranted bool // True if the candidate received vote
}

// Debug helper
func (rf *Raft) reportState() {
	DPrintf("Index: %v, Term: %v, votedFor: %v", rf.me, rf.currentTerm, rf.votedFor)
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	DPrintf("Server %d (Term = %v) get request from server %d - term %d", rf.me, rf.currentTerm, args.MyId, args.Term)
	//rf.reportState()

	rf.mu.Lock()
	defer rf.mu.Unlock()

	candidateUpdated := !rf.hasMoreRecentLogsThan(args.LastLogIndex, args.LastLogTerm) && args.Term >= rf.currentTerm

	if args.Term > rf.currentTerm {
		//DPrintf("Loking on updating when args.Term > rf.currentTerm")

		rf.votedFor = -1
		rf.isLeader = false
		rf.setCurrentTerm(args.Term)
		rf.persist()
	}
	DPrintf("%v > %v : candidateUpdated = %v", rf.me, args.MyId, candidateUpdated)
	if candidateUpdated {
		if rf.votedFor < 0 || rf.votedFor == args.MyId {
			if args.LastLogIndex >= rf.lastApplied {
				//DPrintf("Loking on updating when args.LastLogIndex >= rf.lastApplied")

				reply.VoteGranted = true
				reply.Term = rf.currentTerm
				rf.setCurrentTerm(args.Term)
				rf.votedFor = args.MyId
				DPrintf("Server %d granted vote for server %d", rf.me, args.MyId)
				rf.persist()
				return
			}
		}
	}
	reply.VoteGranted = false
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	//DPrintf("Server %d request vote from server %d", rf.me, server)
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if ok && reply.Term > rf.currentTerm {
		rf.currentTerm = reply.Term
		rf.persist()
		//DPrintf("Reply: %v", reply)
		//DPrintf("Server %d request vote ok", rf.me)
	}
	return ok
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	// Your code here (2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if !rf.isLeader {
		return 0, 0, false
	}

	logEntry := LogEntry{command, rf.currentTerm}

	// Append the command to logs
	rf.logs = append(rf.logs, logEntry)
	rf.persist()
	rf.matchIndexes[rf.me] = len(rf.logs) - 1
	rf.nextIndexes[rf.me] = len(rf.logs)

	DPrintf("------ Leader %v (Term = %v) Start Command  %v  😡😡😡: New Logs = %v --------", rf.me, rf.currentTerm, command, rf.logs)
	go func() {
		rf.tryCommitNewCommand(command)
	}()

	return len(rf.logs), rf.currentTerm, true
}

func (rf *Raft) tryCommitNewCommand(command interface{}) {

	// 2) send AppendEntries to all other servers in parallel
	majoritySuccess := rf.sendAppendEntries()
	if !majoritySuccess {
		//DPrintf("😂Leader %v did not get majoritySuccess", rf.me)
		return
	}
	// the entry has been safely replicated on a majority of the servers
	rf.mu.Lock()

	// Commit!
	newCommit := -1
	//DPrintf("--- Commit: matchIndexes = %v , commitIndex = %v ----", rf.matchIndexes, rf.commitIndex)
	for i := len(rf.logs) - 1; i >= 0; i-- {
		committedServer := 0
		for _, k := range rf.matchIndexes {
			if k >= i {
				committedServer++
			}
		}
		//DPrintf("committedServer = %v >? %v", committedServer, len(rf.matchIndexes)/2)
		//DPrintf("rf.logs[i].Term = %v, rf.currentTerm = %v", rf.logs[i].Term, rf.currentTerm)
		if committedServer > len(rf.matchIndexes)/2 && rf.logs[i].Term == rf.currentTerm {
			newCommit = i + 1
			break
		}
	}

	commitIndexUpdated := false
	if newCommit >= 0 {
		//DPrintf("--- Commit: %v ----", newCommit)
		commitIndexUpdated = rf.updateCommitIndex(newCommit)
	}

	rf.mu.Unlock()
	if commitIndexUpdated {
		//DPrintf("--- tryCommitNewCommand done %v ----", newCommit)
		rf.sendApplyCh()
	}
}

func (rf *Raft) sendApplyCh() {
	rf.mu.Lock()
	lastApplied := rf.lastApplied
	commitIndex := rf.commitIndex
	rf.mu.Unlock()

	for ; lastApplied < commitIndex-1; lastApplied++ {
		rf.sendApplyChWithCommmitIndex(lastApplied + 1)
	}
}

func (rf *Raft) sendApplyChWithCommmitIndex(idx int) {
	DPrintf("\n Start: Server %v (C = %v) sendApplyCh: %v : logs = %v----", rf.me, rf.commitIndex, idx+1, rf.logs)

	rf.mu.Lock()
	applyMsg := ApplyMsg{idx + 1, rf.logs[idx].Command, false, nil}
	rf.mu.Unlock()

	DPrintf("\n Waiting on applyCh Server %v sendApplyCh: %v ----\n\n", rf.me, rf.commitIndex)
	rf.applyCh <- applyMsg

	rf.mu.Lock()
	rf.lastApplied = idx
	rf.mu.Unlock()

	DPrintf("\n Done: Server %v sendApplyCh: %v ----\n\n", rf.me, rf.commitIndex)
}

//
// the tester calls Kill() when a Raft instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (rf *Raft) Kill() {
	// Your code here, if desired.
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd,
	me int,
	persister *Persister,
	applyCh chan ApplyMsg) *Raft {
	//DPrintf("Server %d 😀", me)
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	rf.applyCh = applyCh

	// Your initialization code here (2A, 2B, 2C).
	rf.currentTerm = 0
	rf.votedFor = -1
	rf.logs = make([]LogEntry, 0, 10)
	rf.commitIndex = 0
	rf.lastApplied = -1
	rf.heartbeat = make(chan bool)
	rf.nextIndexes = make([]int, len(rf.peers))
	rf.matchIndexes = make([]int, len(rf.peers))

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	go func() { // Start the election
		periodicallyElect(rf)
	}()

	return rf
}

func periodicallyElect(rf *Raft) {
	// random seed
	s := rand.NewSource(int64(rf.me))
	r := rand.New(s)
	for {
		nSleep := time.Duration(800 + r.Intn(500))
		//DPrintf("Server %d sleeps %d", rf.me, nSleep)
		select {
		case <-rf.heartbeat:
			//DPrintf("server %d get heartbeat 🧡 , continue", rf.me)
			continue // Reset the timeout
		case <-time.After(nSleep * time.Millisecond): // Random timeout between 500 and 1000 ms
			DPrintf("Timeout: Server %v (Term = %v)\n\n", rf.me, rf.currentTerm)
			go func() {
				convertToCandidate(rf)
			}()
		}
	}
}

func convertToCandidate(rf *Raft) {
	if rf.getIsLeader() {
		return
	}
	//rf.requestingVoteMutex.Lock()
	//defer rf.requestingVoteMutex.Unlock()

	var requestArgs RequestVoteArgs
	//DPrintf("Loking on converting to candidate")

	rf.mu.Lock()

	rf.isLeader = false
	requestArgs.MyId = rf.me
	requestArgs.LastLogIndex = len(rf.logs) - 1
	if len(rf.logs) > 0 {
		requestArgs.LastLogTerm = rf.logs[len(rf.logs)-1].Term
	}

	DPrintf(">>>>>> Server %d becomes a candidate, new term: %v, nextIdx = %v <<<<<<<<", rf.me, rf.currentTerm+1, rf.nextIndexes)
	// convert to candidate
	rf.currentTerm++
	requestArgs.Term = rf.currentTerm
	rf.heartbeat <- true // reset election timer
	//DPrintf("Server %d becomes a candidate current Term: %d", rf.me, rf.currentTerm)
	rf.votedFor = rf.me
	rf.persist()

	rf.mu.Unlock()

	votes := 1 // number of votes granted so far
	var wg sync.WaitGroup
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		wg.Add(1)
		go func(i int) {
			DPrintf("Server %d current votes = %v before requesting server %v", rf.me, votes, i)
			defer wg.Done()
			var requestReply RequestVoteReply
			ok := rf.sendRequestVote(i, &requestArgs, &requestReply)
			DPrintf("Server %d ok = %v, granted = %v, after requesting server %v", i, ok, requestReply.VoteGranted, i)
			if ok && requestReply.VoteGranted {
				votes++
				DPrintf("Server %d get vote from server %v, current votes = %v", rf.me, i, votes)
			}
			DPrintf("Server %d current votes = %v after requesting server %v", rf.me, votes, i)
		}(i)
	}
	wg.Wait()
	DPrintf("Server %d get votes: %v, %v, isLeader: %v", rf.me, votes, len(rf.peers)/2, rf.isLeader)
	if votes > len(rf.peers)/2 && !rf.getIsLeader() { // Wins the majority
		DPrintf("%v Become a leader: nextIndexes = %v", rf.me, rf.nextIndexes)
		rf.becomeALeader()
	}
}

func (rf *Raft) hasMoreRecentLogsThan(lastLogIndex int, lastLogTerm int) bool {
	if len(rf.logs) == 0 {
		return false
	}
	myLast := rf.logs[len(rf.logs)-1]
	DPrintf("server %v checking hasMoreRecentLogsThan: myLast.Term = %v, lastLogTerm = %v, mylogs = %v, other = %v", rf.me, myLast.Term, lastLogTerm, len(rf.logs), lastLogIndex+1)
	if myLast.Term != lastLogTerm {
		return myLast.Term > lastLogTerm
	}
	return len(rf.logs) > lastLogIndex+1
}

func (rf *Raft) becomeALeader() {
	rf.setLeader(true)

	rf.mu.Lock()
	// Re-initialize the nextIndexes and matchIndexes
	nextIdx := len(rf.logs)
	for i := range rf.nextIndexes {
		rf.nextIndexes[i] = nextIdx
	}
	for i := range rf.matchIndexes {
		rf.matchIndexes[i] = -1
	}
	rf.mu.Unlock()

	rf.periodicallySendHeartbeats()
}

// Leader sent heartbeats periodically
func (rf *Raft) periodicallySendHeartbeats() {
	for {
		time.Sleep(time.Duration(100) * time.Millisecond) // Send heartbeat every 100ms
		if !rf.getIsLeader() {
			return
		}
		//DPrintf("Leader %d sending heartbeats to server %v", rf.me)
		rf.sendAppendEntriesEmpty()
		//rf.sendAppendEntries(true)
	}
}

func min(x int, y int) int {
	if x < y {
		return x
	}
	return y
}
