// Calculate checksums for each file in a directory-tree,
// suitable for use with management systems such as BigFix and Casper.
//
// Notice the program should be called from the commandline as follows
//  ts8-mac . sha1 -cpuLimit=8
//
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/nlopes/slack"
)

type fInfo struct {
	name    string
	sz      int64
	mode    os.FileMode
	modTime time.Time
}

type item struct {
	File     string
	Checksum string
}

var conf struct {
	Folders     []string `json:"folders"`
	Storage     string   `json:"storage"`
	Ignored     []string `json:"ignored"`
	LogFile     string   `json:"logfile"`
	SlackChatID string   `json:"slack_chat_id"`
	SlackToken  string   `json:"slack_token"`
}

type checksumWorkerFunction func(int, string, *fInfo, error) string

const CHUNKSIZE uint64 = 8192

var wrkQueue = make(chan *fInfo)
var outQueue = make(chan item)
var globalL sync.Mutex
var globalI map[string]string
var globalO = map[string]string{}
var globalDiff []string
var newfiles []string
var ignored []string
var currentRoot string
var ferrors []string
var wg sync.WaitGroup

func inSlice(words []string, word string) bool {
	for _, w := range words {
		if word == w {
			return true
		}
	}
	return false
}

func sendSlack(m string) {
	if conf.SlackChatID != "" {
		log.Printf("Sending slack message '%s'", m)
		api := slack.New(conf.SlackToken)
		api.PostMessage(
			conf.SlackChatID,
			slack.MsgOptionText(m, false),
		)
	}
}

func checkSumSHA1(threadID int, pathname string, fi *fInfo, err error) string {
	var filesize int64 = fi.sz

	file, err := os.Open(pathname)
	if err != nil {
		return fmt.Sprintf("%v", err)
	}

	defer file.Close()

	blocks := uint64(math.Ceil(float64(filesize) / float64(CHUNKSIZE)))

	hash := sha1.New()

	for i := uint64(0); i < blocks; i++ {
		blocksize := int(math.Min(float64(CHUNKSIZE), float64(filesize-int64(i*CHUNKSIZE))))
		buf := make([]byte, blocksize)

		file.Read(buf)
		io.WriteString(hash, string(buf)) // 'tack on' the end
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// This just 'walks' through the filesystem, grabbing fileInfo information; queueing up to the 'Work' input
func walkPathNSum(pathname string, f os.FileInfo, err error) error {
	if f == nil {
		ferrors = append(ferrors, fmt.Sprintf("can't read %s", pathname))
		return nil
	}
	if inSlice(conf.Ignored, currentRoot+f.Name()) {
		//		log.Printf("skipping %s", pathname)
		return filepath.SkipDir
	}
	if f.Mode()&os.ModeSymlink != 0 {
		//		log.Printf("symlink %s", pathname)
		return nil
	}
	if f.IsDir() {
		return nil
	}
	i := &fInfo{name: pathname, sz: f.Size(), mode: f.Mode(), modTime: f.ModTime()}
	wrkQueue <- i
	return nil
}

// Worker function grabs a string from the input Q, uses the checksumWorkerFunction pointer to checksum the file and sends that to the putput Q
func Worker(i int, inq chan *fInfo, outq chan item, cwf checksumWorkerFunction) {
	var fileToCheck *fInfo
	var err error

	for {
		fileToCheck = <-inq
		if fileToCheck == nil {
			wg.Done()
			break
		}
		outq <- item{File: fileToCheck.name, Checksum: cwf(i, fileToCheck.name, fileToCheck, err)}
	}
}

// Outputter outputs the calculated checksum string to the appropriate entity (today, the console; tomorrow a DB)
func Outputter(outq chan item) {
	var out item

	for {
		out = <-outq
		if len(out.File) == 0 {
			break
		}
		globalL.Lock()
		if v, ok := globalI[out.File]; ok {
			if v != out.Checksum {
				globalDiff = append(globalDiff, out.File)
			}
		} else {
			newfiles = append(newfiles, out.File)
		}
		globalO[out.File] = out.Checksum
		globalL.Unlock()
		//		time.Sleep(time.Second * 1)
	}
}

func main() {

	if len(os.Args) < 2 {
		fmt.Println("quick and dirty file integrity checker")
		fmt.Println("needs config.json with content { \"folders\": [\"/folder/a\",\"/folder/b\"], \"ignored\": [\"/folder/a/var\",\"/folder/b/var\"], \"logfile\": \"./changes.log\", \"storage\": \"/safeplace/checksums.json\" }")
		os.Exit(-1)
	}

	byteValue, _ := ioutil.ReadFile(os.Args[1])
	json.Unmarshal(byteValue, &conf)
	byteValue, _ = ioutil.ReadFile(conf.Storage)
	json.Unmarshal(byteValue, &globalI)

	//	var pause string
	var numberCpus = runtime.NumCPU()

	nPtr := flag.Int("cpuLimit", 0, "an int")

	// Assumes that the first argument is a FQDN, no '~' and uses '/'s vs. '\'s
	flag.Parse()

	if *nPtr > 0 {
		runtime.GOMAXPROCS(*nPtr)
		//		fmt.Println("\nWorker threads: changed from ", numberCpus, " to ", *nPtr)
	} else {
		*nPtr = numberCpus
		runtime.GOMAXPROCS(numberCpus)
		//		fmt.Println("\nWorker threads: ", numberCpus)
	}

	// spawn workers
	for i := 0; i < *nPtr; i++ {
		wg.Add(1)
		go Worker(i, wrkQueue, outQueue, checkSumSHA1)
	}

	go Outputter(outQueue)

	for _, root := range conf.Folders {
		//fmt.Printf("walkin %s", root)
		fileInfo, err := os.Lstat(root)
		if err != nil {
			fmt.Println(err)
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(root)
			if err != nil {
				fmt.Println(err)
			}
			//			log.Printf("symlink %s to %s", root, target)
			root = target
		}
		currentRoot = root
		filepath.Walk(root, walkPathNSum)
	}

	for i := 0; i < *nPtr; i++ {
		wrkQueue <- nil
	}
	wg.Wait()
	time.Sleep(3 * time.Second) // outputter

	b, _ := json.Marshal(globalO)

	ioutil.WriteFile(conf.Storage, b, 0644)
	//fmt.Printf("Written %s", conf.Storage)
	if len(newfiles) > 0 || len(globalDiff) > 0 {
		body := "On Rodial live we have modified/new files.\n"
		if len(ferrors) > 0 {
			fmt.Printf("Errors: %v\n", ferrors)
		}
		if len(globalDiff) > 0 {
			body += fmt.Sprintf("Changed files/folders: %v\n", globalDiff)
		}
		if len(newfiles) > 0 {
			body += fmt.Sprintf("New files/folders: %v\n", newfiles)
		}
		sendSlack(body)
	}
	f, err := os.OpenFile(conf.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	defer f.Close()
	log.SetOutput(f)

	if len(ferrors) > 0 {
		log.Printf("Errors: %v", ferrors)
	}
	if len(globalDiff) > 0 {
		log.Printf("Changed files/folders: %v", globalDiff)
	}
	if len(newfiles) > 0 {
		log.Printf("New files/folders: %v", newfiles)
	}
}
