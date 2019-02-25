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
import "math/rand"

// import "bytes"
// import "encoding/gob"

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
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	heartbeat chan bool           // Channel for receiving heartbeat
	isLeader  bool
	applyCh   chan ApplyMsg

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
	DPrintf("Loking on getting state for server %d", rf.me)
	rf.mu.Lock()
	DPrintf("Acquired lock on getting state for server %d", rf.me)
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.isLeader
}

// Check isLeader in thread-safe way
func (rf *Raft) getIsLeader() bool {
	DPrintf("Loking on get isLeader")
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.isLeader
}

// Check isLeader in thread-safe way
func (rf *Raft) getCurrentTerm() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm
}

// Check isLeader in thread-safe way
func (rf *Raft) setLeader(isLeader bool) {
	DPrintf("Loking on set isLeader")
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.isLeader = isLeader
	if isLeader {
		DPrintf("\n NEW LEADER: %d", rf.me)
	}
}

// Return RPC-ok, reply-success
func (rf *Raft) sendAppendEntriesToServer(server int) (bool, bool) {
	var appendEntriesArgs AppendEntriesArgs
	var appendEntriesReply AppendEntriesReply
	appendEntriesArgs.Leader = rf.me
	appendEntriesArgs.Term = rf.getCurrentTerm()
	prevIndex := rf.nextIndexes[server] - 1
	appendEntriesArgs.PrevLogIndex = prevIndex
	DPrintf("prevIndex =  %d", prevIndex)
	if prevIndex >= 0 {
		appendEntriesArgs.PrevLogTerm = rf.logs[prevIndex].Term
	}
	appendEntriesArgs.Entries = rf.logs[prevIndex+1:]
	appendEntriesArgs.LeaderCommitIdx = rf.commitIndex

	callSuccess := rf.peers[server].Call("Raft.AppendEntries", &appendEntriesArgs, &appendEntriesReply)
	return callSuccess, appendEntriesReply.Success
}

func (rf *Raft) keepSendAppendEntriesToServer(idx int) bool {
	for { // Keep sendAppendEntriesToServer until accepted or lost connection
		ok, accepted := rf.sendAppendEntriesToServer(idx)
		if !ok {
			return false
		}
		if accepted {
			return true
		}
		// rejection
		rf.nextIndexes[idx]--
	}
}

// Leader send heartbeats
// return true if get success from majority
func (rf *Raft) sendAppendEntries(empty bool) bool {

	var wg sync.WaitGroup
	nSuccess := 1 // number of successes for AppendEntries

	for i := 0; i < len(rf.peers); i++ {
		if !empty && i == rf.me {
			continue // if not heartbeat, skip leader itself
		}
		wg.Add(1)
		DPrintf("Server %d send heartbeat to server %d", rf.me, i)
		go func(idx int) {
			if rf.keepSendAppendEntriesToServer(idx) {
				nSuccess++
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	DPrintf("send AppendEntries: 🧡Success = %v", nSuccess)
	if empty {
		return true
	}
	return nSuccess > len(rf.peers)/2
}

// AppendEntries handler
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	DPrintf("Server %d get heartbeat from leader %d", rf.me, args.Leader)
	DPrintf("args.Term: %v, my term: %d", args.Term, rf.currentTerm)

	rf.mu.Lock()
	DPrintf("Acquired lock for getting heartbeat on server %d", rf.me)
	reply.Term = rf.currentTerm
	reply.Success = false
	defer rf.mu.Unlock()

	if args.Term < rf.currentTerm {
		DPrintf("Rejects outdated append-entries ... ")
		return
	}
	// Check the term at PrevLogIndex
	prevIndex := args.PrevLogIndex
	prevTerm := args.PrevLogTerm
	if prevIndex >= 0 && len(rf.logs) < prevIndex {
		DPrintf("Rejects mismatch  ")
		return
	}

	reply.Success = true

	if prevIndex >= 0 && rf.logs[prevIndex].Term != prevTerm {
		rf.logs = rf.logs[:prevIndex]
	}
	rf.logs = append(rf.logs, args.Entries...)

	if args.LeaderCommitIdx > rf.commitIndex {
		rf.commitIndex = min(args.LeaderCommitIdx, len(rf.logs)-1)
		DPrintf("💤🌕🌕Set server %v commitIndex as %v", rf.me, rf.commitIndex)
	}

	rf.isLeader = (args.Leader == rf.me)

	rf.currentTerm = args.Term
	rf.votedFor = -1

	rf.heartbeat <- true
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
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	MyTerm       int // candidate's term
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
	CurrentTerm int
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
	DPrintf("Server %d get request from server %d - term %d", rf.me, args.MyId, args.MyTerm)
	rf.reportState()

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if args.MyTerm > rf.currentTerm {
		DPrintf("Loking on updating when args.MyTerm > rf.currentTerm")

		rf.votedFor = -1
		rf.isLeader = false
		rf.currentTerm = args.MyTerm

	}
	candidateUpdated := !rf.hasMoreRecentLogsThan(args.LastLogIndex, args.LastLogTerm) && args.MyTerm >= rf.currentTerm
	DPrintf("candidateUpdated = %v", candidateUpdated)
	if candidateUpdated {
		if rf.votedFor < 0 || rf.votedFor == args.MyId {
			if args.LastLogIndex >= rf.lastApplied {
				DPrintf("Loking on updating when args.LastLogIndex >= rf.lastApplied")

				reply.VoteGranted = true
				reply.CurrentTerm = rf.currentTerm
				rf.currentTerm = args.MyTerm
				rf.votedFor = args.MyId
				DPrintf("Server %d granted vote for server %d", rf.me, args.MyId)

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
	// DPrintf("Server %d request vote from server %d", rf.me, server)
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	if ok {
		DPrintf("Reply: %v", reply)
		DPrintf("Server %d request vote ok", rf.me)
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
	DPrintf("------ Leader %v Start Command  %v  😡😡😡 --------", rf.me, command)
	go func() {
		rf.tryCommitNewCommand(command)
	}()

	return rf.commitIndex, rf.currentTerm, true
}

func (rf *Raft) tryCommitNewCommand(command interface{}) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	logEntry := LogEntry{command, rf.currentTerm}
	// 1) Append the command to logs
	rf.logs = append(rf.logs, logEntry)

	// 2) send AppendEntries to all other servers in parallel
	majoritySuccess := rf.sendAppendEntries(false)
	if majoritySuccess { // the entry has been safely replicated on a majority of the servers
		// Commit!
		rf.commitIndex = len(rf.logs) - 1
		applyMsg := ApplyMsg{rf.me, rf.logs[rf.commitIndex].Command, true, nil}
		rf.applyCh <- applyMsg
	}

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
	DPrintf("Server %d 😀", me)
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.currentTerm = 0
	rf.votedFor = -1
	rf.logs = make([]LogEntry, 0, 10)
	rf.commitIndex = -1
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
		nSleep := time.Duration(400 + r.Intn(500))
		DPrintf("Server %d sleeps %d", rf.me, nSleep)
		select {
		case <-rf.heartbeat:
			DPrintf("server %d get heartbeat, continue", rf.me)
			continue // Reset the timeout
		case <-time.After(nSleep * time.Millisecond): // Random timeout between 500 and 1000 ms
			go func() {
				convertToCandidate(rf)
			}()
		}
	}
}

func convertToCandidate(rf *Raft) {
	var requestArgs RequestVoteArgs
	requestArgs.MyId = rf.me
	DPrintf("Loking on converting to candidate")
	rf.mu.Lock()
	rf.isLeader = false
	DPrintf("Server %d becomes a candidate", rf.me)
	// convert to candidate
	rf.currentTerm++
	rf.heartbeat <- true // reset election timer
	requestArgs.MyTerm = rf.currentTerm
	DPrintf("Server %d becomes a candidate current Term: %d", rf.me, rf.currentTerm)
	rf.votedFor = rf.me
	rf.mu.Unlock()

	votes := 1 // number of votes granted so far
	var wg sync.WaitGroup
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var requestReply RequestVoteReply
			ok := rf.sendRequestVote(i, &requestArgs, &requestReply)
			if ok && requestReply.VoteGranted {
				votes++
			}
		}(i)
	}
	wg.Wait()
	DPrintf("Server %d get votes: %v, %v", rf.me, votes, len(rf.peers)/2)
	if votes > len(rf.peers)/2 { // Wins the majority
		rf.becomeALeader()
	}
}

func (rf *Raft) hasMoreRecentLogsThan(lastLogIndex int, lastLogTerm int) bool {
	DPrintf("server %v checking hasMoreRecentLogsThan: myLogs = %v", rf.me, rf.logs)
	if len(rf.logs) == 0 {
		return false
	}
	myLast := rf.logs[len(rf.logs)-1]
	if myLast.Term != lastLogTerm {
		return myLast.Term > lastLogTerm
	}
	return len(rf.logs) > lastLogIndex+1
}

func (rf *Raft) becomeALeader() {
	rf.setLeader(true)
	// Re-initialize the nextIndexes and matchIndexes
	nextIdx := len(rf.logs)
	for i := range rf.nextIndexes {
		rf.nextIndexes[i] = nextIdx
	}
	for i := range rf.matchIndexes {
		rf.matchIndexes[i] = 0
	}

	rf.periodicallySendHeartbeats()
}

// Leader sent heartbeats periodically
func (rf *Raft) periodicallySendHeartbeats() {
	DPrintf("Leader %d start sending heartbeats", rf.me)
	for {
		time.Sleep(time.Duration(100) * time.Millisecond) // Send heartbeat every 100ms
		if !rf.getIsLeader() {
			return
		}

		rf.sendAppendEntries(true)
	}
}

func min(x int, y int) int {
	if x < y {
		return x
	}
	return y
}
