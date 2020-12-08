/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package dthrift

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/apache/thrift/lib/go/thrift"
	"mosn.io/mosn/pkg/protocol"
	"mosn.io/mosn/pkg/types"
	"mosn.io/pkg/buffer"
)

// Decoder is heavy and caches to improve performance.
// Avoid allocating 4k memory every time you create an object
var (
	tTransport *thrift.StreamTransport
	tProtocol  *thrift.TBinaryProtocol
)

func decodeFrame(ctx context.Context, data types.IoBuffer) (cmd interface{}, err error) {
	// convert data to dubbo frame
	dataBytes := data.Bytes()
	frame := &Frame{
		Header: Header{
			CommonHeader: protocol.CommonHeader{},
		},
	}

	//decode frame header: duplicate message length
	messageLen := binary.BigEndian.Uint32(dataBytes[:MessageLenSize])
	//ignore first message length
	body := dataBytes[MessageLenSize : MessageLenSize+messageLen]
	//total frame length
	frame.FrameLength = messageLen + MessageLenSize
	// decode magic
	frame.Magic = body[:MagicLen]
	//decode message length
	frame.MessageLength = binary.BigEndian.Uint32(body[MessageLenIdx : MessageLenIdx+MessageLenSize])
	//decode header length
	frame.HeaderLength = binary.BigEndian.Uint16(body[MessageHeaderLenIdx : MessageHeaderLenIdx+MessageHeaderLenSize])
	//header content + message content
	frame.payload = body[HeaderIdx:]

	frame.rawData = dataBytes[:frame.FrameLength]

	meta := make(map[string]string)

	tTransport = thrift.NewStreamTransportR(buffer.NewIoBufferBytes(frame.payload))
	defer tTransport.Close()
	tProtocol = thrift.NewTBinaryProtocolTransport(tTransport)
	defer tProtocol.Flush(ctx)

	err = decodeHeader(ctx, frame, meta)
	if err != nil {
		return nil, err
	}

	err = decodeMessage(ctx, frame, meta)
	if err != nil {
		return nil, err
	}

	//is request
	if frame.Direction == EventRequest {
		// service aware
		meta, err := getServiceAwareMeta(ctx, frame, meta)
		if err != nil {
			return nil, err
		}
		for k, v := range meta {
			frame.Set(k, v)
		}
	}

	frame.data = buffer.NewIoBufferBytes(frame.rawData)
	frame.content = buffer.NewIoBufferBytes(frame.payload)
	data.Drain(int(frame.FrameLength))
	return frame, nil
}

func decodeMessage(ctx context.Context, frame *Frame, meta map[string]string) error {
	//method name, type, seqId
	name, tType, seqId, err := tProtocol.ReadMessageBegin()

	frame.SeqId = seqId
	meta[MethodNameHeader] = name

	if err != nil {
		return fmt.Errorf("[xprotocol][thrift] decode message body fail: %v", err)
	}
	// check type
	if tType == 1 {
		frame.Direction = EventRequest
	} else {
		frame.Direction = EventResponse
	}
	err = tProtocol.ReadMessageEnd()
	if err != nil {
		return fmt.Errorf("[xprotocol][thrift] decode message body fail: %v", err)
	}
	return nil
}

func decodeHeader(ctx context.Context, frame *Frame, meta map[string]string) error {
	serviceName, err := tProtocol.ReadString()
	if err != nil {
		return fmt.Errorf("[xprotocol][thrift] decode service path fail: %v", err)
	}
	frame.ServiceName = serviceName
	meta[ServiceNameHeader] = serviceName
	//decode requestId
	id, err := tProtocol.ReadI64()
	if err != nil {
		return fmt.Errorf("[xprotocol][thrift] decode requestId fail: %v", err)
	}
	frame.Id = uint64(id)
	return nil
}

func getServiceAwareMeta(ctx context.Context, frame *Frame, meta map[string]string) (map[string]string, error) {
	//nothing to do
	return meta, nil
}
