/***** BEGIN LICENSE BLOCK *****
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this file,
# You can obtain one at http://mozilla.org/MPL/2.0/.
#
# The Initial Developer of the Original Code is the Mozilla Foundation.
# Portions created by the Initial Developer are Copyright (C) 2012
# the Initial Developer. All Rights Reserved.
#
# Contributor(s):
#   Rob Miller (rmiller@mozilla.com)
#   Mike Trinkala (trink@mozilla.com)
#
# ***** END LICENSE BLOCK *****/

package file

import (
	"bytes"
	"code.google.com/p/gomock/gomock"
	"code.google.com/p/goprotobuf/proto"
	"encoding/json"
	"fmt"
	. "github.com/mozilla-services/heka/pipeline"
	pipeline_ts "github.com/mozilla-services/heka/pipeline/testsupport"
	plugins_ts "github.com/mozilla-services/heka/plugins/testsupport"
	gs "github.com/rafrombrc/gospec/src/gospec"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

func FileOutputSpec(c gs.Context) {
	t := new(pipeline_ts.SimpleT)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	oth := plugins_ts.NewOutputTestHelper(ctrl)
	var wg sync.WaitGroup
	inChan := make(chan *PipelinePack, 1)
	pConfig := NewPipelineConfig(nil)

	c.Specify("A FileOutput", func() {
		fileOutput := new(FileOutput)

		tmpFileName := fmt.Sprintf("fileoutput-test-%d", time.Now().UnixNano())
		tmpFilePath := fmt.Sprint(os.TempDir(), string(os.PathSeparator),
			tmpFileName)
		config := fileOutput.ConfigStruct().(*FileOutputConfig)
		config.Path = tmpFilePath

		msg := pipeline_ts.GetTestMessage()
		pack := NewPipelinePack(pConfig.InputRecycleChan())
		pack.Message = msg
		pack.Decoded = true

		toString := func(outData interface{}) string {
			return string(*(outData.(*[]byte)))
		}

		c.Specify("correctly formats text output", func() {
			err := fileOutput.Init(config)
			defer os.Remove(tmpFilePath)
			c.Assume(err, gs.IsNil)
			outData := make([]byte, 0, 20)

			c.Specify("by default", func() {
				err = fileOutput.handleMessage(pack, &outData)
				c.Expect(err, gs.IsNil)
				c.Expect(toString(&outData), gs.Equals, *msg.Payload+"\n")
			})

			c.Specify("w/ a prepended timestamp when specified", func() {
				fileOutput.prefix_ts = true
				err = fileOutput.handleMessage(pack, &outData)
				c.Expect(err, gs.IsNil)
				// Test will fail if date flips btn handleMessage call and
				// todayStr calculation... should be extremely rare.
				todayStr := time.Now().Format("[2006/Jan/02:")
				strContents := toString(&outData)
				payload := *msg.Payload
				c.Expect(strContents, pipeline_ts.StringContains, payload)
				c.Expect(strContents, pipeline_ts.StringStartsWith, todayStr)
			})

			c.Specify("even when payload is nil", func() {
				pack.Message.Payload = nil
				err = fileOutput.handleMessage(pack, &outData)
				c.Expect(err, gs.IsNil)
				strContents := toString(&outData)
				c.Expect(strContents, gs.Equals, "\n")
			})

			c.Specify("payload is nil and with a timestamp", func() {
				pack.Message.Payload = nil
				fileOutput.prefix_ts = true
				err = fileOutput.handleMessage(pack, &outData)
				c.Expect(err, gs.IsNil)
				// Test will fail if date flips btn handleMessage call and
				// todayStr calculation... should be extremely rare.
				todayStr := time.Now().Format("[2006/Jan/02:")
				strContents := toString(&outData)
				c.Expect(strings.HasPrefix(strContents, todayStr), gs.IsTrue)
				c.Expect(strings.HasSuffix(strContents, " \n"), gs.IsTrue)
			})
		})

		c.Specify("correctly formats JSON output", func() {
			config.Format = "json"
			err := fileOutput.Init(config)
			defer os.Remove(tmpFilePath)
			c.Assume(err, gs.IsNil)
			outData := make([]byte, 0, 200)

			c.Specify("when specified", func() {
				fileOutput.handleMessage(pack, &outData)
				msgJson, err := json.Marshal(pack.Message)
				c.Assume(err, gs.IsNil)
				c.Expect(toString(&outData), gs.Equals, string(msgJson)+"\n")
			})

			c.Specify("and with a timestamp", func() {
				fileOutput.prefix_ts = true
				fileOutput.handleMessage(pack, &outData)
				// Test will fail if date flips btn handleMessage call and
				// todayStr calculation... should be extremely rare.
				todayStr := time.Now().Format("[2006/Jan/02:")
				strContents := toString(&outData)
				msgJson, err := json.Marshal(pack.Message)
				c.Assume(err, gs.IsNil)
				c.Expect(strContents, pipeline_ts.StringContains, string(msgJson)+"\n")
				c.Expect(strContents, pipeline_ts.StringStartsWith, todayStr)
			})
		})

		c.Specify("correctly formats protocol buffer stream output", func() {
			config.Format = "protobufstream"
			err := fileOutput.Init(config)
			defer os.Remove(tmpFilePath)
			c.Assume(err, gs.IsNil)
			outData := make([]byte, 0, 200)

			c.Specify("when specified and timestamp ignored", func() {
				fileOutput.prefix_ts = true
				err := fileOutput.handleMessage(pack, &outData)
				c.Expect(err, gs.IsNil)
				b := []byte{30, 2, 8, uint8(proto.Size(pack.Message)), 31, 10, 16} // sanity check the header and the start of the protocol buffer
				c.Expect(bytes.Equal(b, outData[:len(b)]), gs.IsTrue)
			})
		})

		c.Specify("processes incoming messages", func() {
			err := fileOutput.Init(config)
			defer os.Remove(tmpFilePath)
			c.Assume(err, gs.IsNil)
			// Save for comparison.
			payload := fmt.Sprintf("%s\n", pack.Message.GetPayload())

			oth.MockOutputRunner.EXPECT().InChan().Return(inChan)
			wg.Add(1)
			go fileOutput.receiver(oth.MockOutputRunner, &wg)
			inChan <- pack
			close(inChan)
			outBatch := <-fileOutput.batchChan
			wg.Wait()
			c.Expect(string(outBatch), gs.Equals, payload)
		})

		c.Specify("Init halts if basedirectory is not writable", func() {
			tmpdir := filepath.Join(os.TempDir(), "tmpdir")
			err := os.MkdirAll(tmpdir, 0400)
			c.Assume(err, gs.IsNil)
			config.Path = tmpdir
			err = fileOutput.Init(config)
			c.Assume(err, gs.Not(gs.IsNil))
		})

		c.Specify("commits to a file", func() {
			outStr := "Write me out to the log file"
			outBytes := []byte(outStr)

			c.Specify("with default settings", func() {
				err := fileOutput.Init(config)
				defer os.Remove(tmpFilePath)
				c.Assume(err, gs.IsNil)

				// Start committer loop
				wg.Add(1)
				go fileOutput.committer(oth.MockOutputRunner, &wg)

				// Feed and close the batchChan
				go func() {
					fileOutput.batchChan <- outBytes
					_ = <-fileOutput.backChan // clear backChan to prevent blocking
					close(fileOutput.batchChan)
				}()

				wg.Wait()
				// Wait for the file close operation to happen.
				//for ; err == nil; _, err = fileOutput.file.Stat() {
				//}

				tmpFile, err := os.Open(tmpFilePath)
				defer tmpFile.Close()
				c.Assume(err, gs.IsNil)
				contents, err := ioutil.ReadAll(tmpFile)
				c.Assume(err, gs.IsNil)
				c.Expect(string(contents), gs.Equals, outStr)
			})

			c.Specify("with different Perm settings", func() {
				config.Perm = "600"
				err := fileOutput.Init(config)
				defer os.Remove(tmpFilePath)
				c.Assume(err, gs.IsNil)

				// Start committer loop
				wg.Add(1)
				go fileOutput.committer(oth.MockOutputRunner, &wg)

				// Feed and close the batchChan
				go func() {
					fileOutput.batchChan <- outBytes
					_ = <-fileOutput.backChan // clear backChan to prevent blocking
					close(fileOutput.batchChan)
				}()

				wg.Wait()
				// Wait for the file close operation to happen.
				//for ; err == nil; _, err = fileOutput.file.Stat() {
				//}

				tmpFile, err := os.Open(tmpFilePath)
				defer tmpFile.Close()
				c.Assume(err, gs.IsNil)
				fileInfo, err := tmpFile.Stat()
				c.Assume(err, gs.IsNil)
				fileMode := fileInfo.Mode()
				if runtime.GOOS == "windows" {
					c.Expect(fileMode.String(), pipeline_ts.StringContains, "-rw-rw-rw-")
				} else {
					// 7 consecutive dashes implies no perms for group or other
					c.Expect(fileMode.String(), pipeline_ts.StringContains, "-------")
				}
			})
		})

		c.Specify("honors folder_perm setting", func() {
			config.FolderPerm = "750"
			config.Path = filepath.Join(tmpFilePath, "subfile")
			err := fileOutput.Init(config)
			defer os.Remove(config.Path)
			c.Assume(err, gs.IsNil)

			fi, err := os.Stat(tmpFilePath)
			c.Expect(fi.IsDir(), gs.IsTrue)
			c.Expect(fi.Mode().Perm(), gs.Equals, os.FileMode(0750))
		})

		c.Specify("that starts receiving w/ a flush interval", func() {
			config.FlushInterval = 100000000 // We'll trigger the timer manually.
			inChan := make(chan *PipelinePack)
			oth.MockOutputRunner.EXPECT().InChan().Return(inChan)
			timerChan := make(chan time.Time)

			msg2 := pipeline_ts.GetTestMessage()
			pack2 := NewPipelinePack(pConfig.InputRecycleChan())
			pack2.Message = msg2

			recvWithConfig := func(config *FileOutputConfig) {
				err := fileOutput.Init(config)
				c.Assume(err, gs.IsNil)
				wg.Add(1)
				go fileOutput.receiver(oth.MockOutputRunner, &wg)
				runtime.Gosched() // Yield so we can overwrite the timerChan.
				fileOutput.timerChan = timerChan
			}

			cleanUp := func() {
				close(inChan)
				wg.Done()
			}

			c.Specify("honors flush interval", func() {
				recvWithConfig(config)
				defer cleanUp()
				inChan <- pack
				select {
				case _ = <-fileOutput.batchChan:
					c.Expect("", gs.Equals, "fileOutput.batchChan should NOT have fired yet")
				default:
				}
				timerChan <- time.Now()
				select {
				case _ = <-fileOutput.batchChan:
				default:
					c.Expect("", gs.Equals, "fileOutput.batchChan SHOULD have fired by now")
				}
			})

			c.Specify("honors flush interval AND flush count", func() {
				config.FlushCount = 2
				recvWithConfig(config)
				defer cleanUp()
				inChan <- pack

				select {
				case <-fileOutput.batchChan:
					c.Expect("", gs.Equals, "fileOutput.batchChan should NOT have fired yet")
				default:
				}

				timerChan <- time.Now()
				select {
				case <-fileOutput.batchChan:
					c.Expect("", gs.Equals, "fileOutput.batchChan should NOT have fired yet")
				default:
				}

				inChan <- pack2
				runtime.Gosched()
				select {
				case <-fileOutput.batchChan:
				default:
					c.Expect("", gs.Equals, "fileOutput.batchChan SHOULD have fired by now")
				}
			})

			c.Specify("honors flush interval OR flush count", func() {
				config.FlushCount = 2
				config.FlushOperator = "OR"
				recvWithConfig(config)
				defer cleanUp()
				inChan <- pack

				select {
				case <-fileOutput.batchChan:
					c.Expect("", gs.Equals, "fileOutput.batchChan should NOT have fired yet")
				default:
				}

				c.Specify("when interval triggers first", func() {
					timerChan <- time.Now()
					select {
					case <-fileOutput.batchChan:
					default:
						c.Expect("", gs.Equals, "fileOutput.batchChan SHOULD have fired by now")
					}
				})

				c.Specify("when count triggers first", func() {
					inChan <- pack2
					runtime.Gosched()
					select {
					case <-fileOutput.batchChan:
					default:
						c.Expect("", gs.Equals, "fileOutput.batchChan SHOULD have fired by now")
					}
				})
			})
		})
	})
}
