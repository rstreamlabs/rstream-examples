package webrtc

import (
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
)

func (e *associatedStreamBandwidthEstimator) WriteRTCP(
	packets []rtcp.Packet,
	attributes interceptor.Attributes,
) error {
	normalized := packets
	copied := false
	type lossObservation struct {
		reported int
		lost     int
	}
	observations := make([]lossObservation, 0, len(packets))
	for index, packet := range packets {
		feedback, ok := packet.(*rtcp.TransportLayerCC)
		if !ok {
			continue
		}
		reported, lost, valid := e.recordTransportCCFeedback(feedback)
		if valid {
			observations = append(observations, lossObservation{
				reported: reported,
				lost:     lost,
			})
		}
		trimmed, changed := trimTransportCCPadding(feedback)
		if !changed {
			continue
		}
		if !copied {
			normalized = append([]rtcp.Packet(nil), packets...)
			copied = true
		}
		normalized[index] = trimmed
	}
	if err := e.SendSideBWE.WriteRTCP(normalized, attributes); err != nil {
		return err
	}
	if e.lossGuard == nil {
		return nil
	}
	for _, observation := range observations {
		target, changed := e.lossGuard.observe(
			observation.reported,
			observation.lost,
			e.effectiveMediaBitrate(e.SendSideBWE.GetTargetBitrate()),
		)
		if changed {
			e.deliverEffectiveBitrate(target)
		}
	}
	return nil
}

func (e *associatedStreamBandwidthEstimator) recordTransportCCFeedback(
	feedback *rtcp.TransportLayerCC,
) (reported int, lost int, valid bool) {
	reported, lost, padding, valid := transportCCStatusCounts(feedback)
	e.twccFeedbackPackets.Add(1)
	if !valid {
		e.twccMalformedFeedback.Add(1)
		return 0, 0, false
	}
	e.twccReportedStatuses.Add(uint64(reported))
	e.twccReportedLost.Add(uint64(lost))
	e.twccPaddingStatuses.Add(uint64(padding))
	return reported, lost, true
}

func transportCCStatusCounts(
	feedback *rtcp.TransportLayerCC,
) (reported int, lost int, padding int, valid bool) {
	if feedback == nil {
		return 0, 0, 0, false
	}
	reported = int(feedback.PacketStatusCount)
	remaining := reported
	covered := 0
	for _, rawChunk := range feedback.PacketChunks {
		switch chunk := rawChunk.(type) {
		case *rtcp.RunLengthChunk:
			if chunk == nil {
				return 0, 0, 0, false
			}
			length := int(chunk.RunLength)
			covered += length
			withinReport := min(length, remaining)
			if chunk.PacketStatusSymbol == rtcp.TypeTCCPacketNotReceived {
				lost += withinReport
			}
			remaining -= withinReport
		case *rtcp.StatusVectorChunk:
			if chunk == nil {
				return 0, 0, 0, false
			}
			covered += len(chunk.SymbolList)
			withinReport := min(len(chunk.SymbolList), remaining)
			for _, symbol := range chunk.SymbolList[:withinReport] {
				if symbol == rtcp.TypeTCCPacketNotReceived {
					lost++
				}
			}
			remaining -= withinReport
		default:
			return 0, 0, 0, false
		}
	}
	if remaining != 0 {
		return 0, 0, 0, false
	}
	return reported, lost, max(0, covered-reported), true
}

// trimTransportCCPadding removes status-vector padding that lies beyond
// PacketStatusCount. Pion interceptor <= v0.1.47 includes those padding slots
// in loss estimation, which can turn a healthy 2% loss profile into a false
// double-digit loss signal. This is the local equivalent of upstream PR #414
// and can be removed after a release containing that fix is adopted.
func trimTransportCCPadding(
	feedback *rtcp.TransportLayerCC,
) (*rtcp.TransportLayerCC, bool) {
	if feedback == nil {
		return feedback, false
	}
	remaining := int(feedback.PacketStatusCount)
	chunks := make([]rtcp.PacketStatusChunk, 0, len(feedback.PacketChunks))
	changed := false
	for _, rawChunk := range feedback.PacketChunks {
		if remaining == 0 {
			changed = true
			break
		}
		switch chunk := rawChunk.(type) {
		case *rtcp.RunLengthChunk:
			if chunk == nil {
				return feedback, false
			}
			copyOfChunk := *chunk
			length := int(copyOfChunk.RunLength)
			if length > remaining {
				copyOfChunk.RunLength = uint16(remaining)
				length = remaining
				changed = true
			}
			chunks = append(chunks, &copyOfChunk)
			remaining -= length
		case *rtcp.StatusVectorChunk:
			if chunk == nil {
				return feedback, false
			}
			copyOfChunk := *chunk
			length := len(copyOfChunk.SymbolList)
			if length > remaining {
				copyOfChunk.SymbolList = append([]uint16(nil), copyOfChunk.SymbolList[:remaining]...)
				length = remaining
				changed = true
			} else {
				copyOfChunk.SymbolList = append([]uint16(nil), copyOfChunk.SymbolList...)
			}
			chunks = append(chunks, &copyOfChunk)
			remaining -= length
		default:
			return feedback, false
		}
	}
	if remaining != 0 || !changed {
		return feedback, false
	}
	copyOfFeedback := *feedback
	copyOfFeedback.PacketChunks = chunks
	return &copyOfFeedback, true
}
