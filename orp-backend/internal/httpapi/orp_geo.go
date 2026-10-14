package httpapi

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

const maxGeoSampleEvents = 10000

type maxMindCity struct {
	Country struct {
		ISOCode string            "maxminddb:\"iso_code\""
		Names   map[string]string "maxminddb:\"names\""
	} "maxminddb:\"country\""
	City struct {
		Names map[string]string "maxminddb:\"names\""
	} "maxminddb:\"city\""
	Location struct {
		Latitude  float64 "maxminddb:\"latitude\""
		Longitude float64 "maxminddb:\"longitude\""
	} "maxminddb:\"location\""
}

type geoPoint struct {
	name, countryCode   string
	longitude, latitude float64
	requests            int
}

type geoFlow struct {
	sourceKey string
	centerID  int64
	requests  int
}

func (s *Server) dashboardGeography(events []accessEvent) map[string]any {
	centerRowsOut := []map[string]any{}
	empty := func(state, reason string) map[string]any {
		return map[string]any{"state": state, "reason": reason, "sampleLimit": maxGeoSampleEvents, "points": []any{}, "centers": centerRowsOut, "flows": []any{}, "unknownRequests": 0}
	}
	nodes, err := s.orpLoad(context.Background(), "nodes")
	if err != nil {
		return empty("error", "读取节点中心映射失败")
	}
	nodeCenters := map[int64]int64{}
	for _, node := range nodes {
		nodeID, _ := resourceNumber(node["id"])
		centerID, _ := resourceNumber(node["centerId"])
		nodeCenters[nodeID] = centerID
	}
	centerRows, err := s.orpLoad(context.Background(), "centers")
	if err != nil {
		return empty("error", "读取中心地理位置失败")
	}
	type location struct {
		id                  int64
		name                string
		longitude, latitude float64
	}
	centers := map[int64]location{}
	for _, item := range centerRows {
		id, _ := resourceNumber(item["id"])
		lat, latOK := numericFloat(item["latitude"])
		lon, lonOK := numericFloat(item["longitude"])
		if latOK && lonOK && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
			centers[id] = location{id: id, name: toString(item["name"]), longitude: lon, latitude: lat}
		}
	}
	for _, center := range centers {
		centerRowsOut = append(centerRowsOut, map[string]any{"id": center.id, "name": center.name, "coord": []float64{center.longitude, center.latitude}})
	}
	sort.Slice(centerRowsOut, func(i, j int) bool { return centerRowsOut[i]["id"].(int64) < centerRowsOut[j]["id"].(int64) })
	path, source := s.activeGeoIPDatabasePath()
	if path == "" {
		return empty("disabled", "尚未导入或配置 GeoIP City 数据库；已显示中心位置")
	}
	reader, err := maxminddb.Open(path)
	if err != nil {
		if source == "managed" {
			return empty("error", "已导入的 GeoIP 数据库无法打开")
		}
		return empty("error", "环境变量配置的 GeoIP 数据库无法打开")
	}
	defer reader.Close()
	if !isGeoIPCityDatabase(reader.Metadata.DatabaseType) {
		return empty("error", "GeoIP 数据库不是 City 类型")
	}
	if len(events) > maxGeoSampleEvents {
		sort.Slice(events, func(i, j int) bool { return events[i].TS > events[j].TS })
		events = events[:maxGeoSampleEvents]
	}
	points := map[string]*geoPoint{}
	flows := map[string]*geoFlow{}
	unknown := 0
	for _, event := range events {
		rawIP := strings.TrimSpace(event.RemoteAddr)
		if host, err := netip.ParseAddrPort(rawIP); err == nil {
			rawIP = host.String()
		}
		ip, err := netip.ParseAddr(rawIP)
		if err != nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			unknown++
			continue
		}
		var record maxMindCity
		if err := reader.Lookup(ip.Unmap()).Decode(&record); err != nil || record.Country.ISOCode == "" || record.Location.Latitude < -90 || record.Location.Latitude > 90 || record.Location.Longitude < -180 || record.Location.Longitude > 180 {
			unknown++
			continue
		}
		name := record.City.Names["en"]
		if name == "" {
			name = record.Country.Names["en"]
		}
		if name == "" {
			name = record.Country.ISOCode
		}
		key := record.Country.ISOCode + ":" + name + ":" + strconv.FormatFloat(record.Location.Latitude, 'f', 2, 64) + ":" + strconv.FormatFloat(record.Location.Longitude, 'f', 2, 64)
		point := points[key]
		if point == nil {
			point = &geoPoint{name: name, countryCode: record.Country.ISOCode, longitude: record.Location.Longitude, latitude: record.Location.Latitude}
			points[key] = point
		}
		point.requests++
		centerID := nodeCenters[event.NodeID]
		if _, ok := centers[centerID]; ok {
			flowKey := key + ":" + strconv.FormatInt(centerID, 10)
			flow := flows[flowKey]
			if flow == nil {
				flow = &geoFlow{sourceKey: key, centerID: centerID}
				flows[flowKey] = flow
			}
			flow.requests++
		}
	}
	orderedPoints := make([]*geoPoint, 0, len(points))
	for _, point := range points {
		orderedPoints = append(orderedPoints, point)
	}
	sort.Slice(orderedPoints, func(i, j int) bool { return orderedPoints[i].requests > orderedPoints[j].requests })
	if len(orderedPoints) > 200 {
		orderedPoints = orderedPoints[:200]
	}
	pointRows := make([]map[string]any, 0, len(orderedPoints))
	for _, point := range orderedPoints {
		pointRows = append(pointRows, map[string]any{"name": point.name, "countryCode": point.countryCode, "coord": []float64{point.longitude, point.latitude}, "requests": point.requests})
	}
	flowRows := make([]map[string]any, 0, len(flows))
	for _, flow := range flows {
		point, center := points[flow.sourceKey], centers[flow.centerID]
		if point != nil {
			flowRows = append(flowRows, map[string]any{"source": point.name, "center": center.name, "coords": [][]float64{{point.longitude, point.latitude}, {center.longitude, center.latitude}}, "requests": flow.requests})
		}
	}
	sort.Slice(flowRows, func(i, j int) bool { return flowRows[i]["requests"].(int) > flowRows[j]["requests"].(int) })
	if len(flowRows) > 500 {
		flowRows = flowRows[:500]
	}
	return map[string]any{"state": "ready", "sampledEvents": len(events), "sampleLimit": maxGeoSampleEvents, "points": pointRows, "centers": centerRowsOut, "flows": flowRows, "unknownRequests": unknown}
}

func numericFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case string:
		parsed, err := strconv.ParseFloat(number, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
