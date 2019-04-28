package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	nsq "github.com/bitly/go-nsq"
	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var fatalErr error

func fatal(e error) {
	fmt.Println(e)
	flag.PrintDefaults()
	fatalErr = e
}

const updateDuration = 1 * time.Second

func main() {
	var countsLook sync.Mutex
	var counts map[string]int
	defer func() {
		if fatalErr != nil {
			os.Exit(1)
		}
	}()
	log.Println("データベースに接続します...")
	db, err := mgo.Dial("localhost")
	if err != nil {
		fatal(err)
		return
	}
	defer func() {
		log.Println("データベースの接続を閉じます...")
		db.Close()
	}()
	pollData := db.DB("ballots").C("polls")
	//NSQに接続
	log.Println("NSQに接続ます")
	q, err := nsq.NewConsumer("votes", "counter", nsq.NewConfig())
	if err != nil {
		fatal(err)
		return
	}

	//NSQからメッセージを受け取った時の処理
	q.AddHandler(nsq.HandlerFunc(func(m *nsq.Message) error {
		countsLook.Lock()
		defer countsLook.Unlock()
		if counts == nil {
			counts = make(map[string]int)
		}
		vote := string(m.Body)
		counts[vote]++
		return nil
	}))
	if err := q.ConnectToNSQLookupd("localhost:4161"); err != nil {
		fatal(err)
		return
	}
	log.Println("NSQ上での投票を待機します...")
	var updater *time.Timer
	updater = time.AfterFunc(updateDuration, func() {
		countsLook.Lock()
		defer countsLook.Unlock()
		if len(counts) == 0 {
			log.Println("新しい投票はありません。データベースの更新をスキップします")
		} else {
			log.Println("データベースを更新します...")
			log.Println(counts)
			ok := true
			for option, count := range counts {
				sel := bson.M{"options": bson.M{"$in": []string{option}}}
				up := bson.M{"$inc": bson.M{"results." + option: count}}
				if _, err := pollData.UpdateAll(sel, up); err != nil {
					log.Println("更新に失敗しました:", err)
					ok = false
					continue
				}
				counts[option] = 0
			}
			if ok {
				log.Println("データベースの更新が完了しました")
				counts = nil // 得票数をリセットします
			}
		}
		updater.Reset(updateDuration)
	})
}
