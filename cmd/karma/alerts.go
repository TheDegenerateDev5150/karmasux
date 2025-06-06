package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/fvbommel/sortorder"
	"github.com/rs/zerolog/log"

	"github.com/prymitive/karma/internal/alertmanager"
	"github.com/prymitive/karma/internal/config"
	"github.com/prymitive/karma/internal/filters"
	"github.com/prymitive/karma/internal/models"
	"github.com/prymitive/karma/internal/slices"
	"github.com/prymitive/karma/internal/uri"
)

func getFiltersFromQuery(filterStrings []string) []filters.FilterT {
	matchFilters := []filters.FilterT{}
	for _, filterExpression := range filterStrings {
		f := filters.NewFilter(filterExpression)
		matchFilters = append(matchFilters, f)
	}
	return matchFilters
}

func countLabel(countStore map[string]map[string]int, key, val string) {
	if _, found := countStore[key]; !found {
		countStore[key] = make(map[string]int)
	}
	countStore[key][val]++
}

func countersToLabelStats(counters map[string]map[string]int) models.LabelNameStatsList {
	data := make(models.LabelNameStatsList, 0, len(counters))

	for name, valueMap := range counters {
		values := make(models.LabelValueStatsList, 0, len(valueMap))
		nameStats := models.LabelNameStats{
			Name:   name,
			Values: values,
		}

		for value, hits := range valueMap {
			nameStats.Hits += hits
			valueStats := models.LabelValueStats{
				Value: value,
				Raw:   fmt.Sprintf("%s=%s", name, value),
				Hits:  hits,
			}
			nameStats.Values = append(nameStats.Values, valueStats)
		}

		// now that we have total hits we can calculate %
		var totalPercent int
		for i, value := range nameStats.Values {
			nameStats.Values[i].Percent = int(math.Floor((float64(value.Hits) / float64(nameStats.Hits)) * 100.0))
			totalPercent += nameStats.Values[i].Percent
		}
		sort.Sort(nameStats.Values)
		for totalPercent < 100 {
			for i := range nameStats.Values {
				nameStats.Values[i].Percent++
				totalPercent++
				if totalPercent >= 100 {
					break
				}
			}
		}

		// now that we have all % and values are sorted we can calculate offsets
		offset := 0
		for i, value := range nameStats.Values {
			nameStats.Values[i].Offset = offset
			offset += value.Percent
		}
		data = append(data, nameStats)
	}

	sort.Sort(data)

	return data
}

func getUpstreams() models.AlertmanagerAPISummary {
	summary := models.AlertmanagerAPISummary{
		Instances: []models.AlertmanagerAPIStatus{},
	}

	clusters := map[string][]string{}
	upstreams := alertmanager.GetAlertmanagers()
	for _, upstream := range upstreams {
		members := upstream.ClusterMemberNames()

		if _, found := clusters[upstream.Cluster]; !found {
			clusters[upstream.Cluster] = members
		}

		u := models.AlertmanagerAPIStatus{
			Name:            upstream.Name,
			URI:             upstream.InternalURI(),
			PublicURI:       upstream.PublicURI(),
			ReadOnly:        upstream.ReadOnly,
			Headers:         map[string]string{},
			CORSCredentials: upstream.CORSCredentials,
			Error:           upstream.Error(),
			Version:         upstream.Version(),
			Cluster:         upstream.Cluster,
			ClusterMembers:  members,
		}
		if !upstream.ProxyRequests {
			for k, v := range uri.HeadersForBasicAuth(upstream.URI) {
				u.Headers[k] = v
			}
			for k, v := range upstream.HTTPHeaders {
				u.Headers[k] = v
			}
		}
		summary.Instances = append(summary.Instances, u)

		summary.Counters.Total++
		if upstream.IsHealthy() {
			summary.Counters.Healthy++
		} else {
			summary.Counters.Failed++
		}
	}
	summary.Clusters = clusters

	return summary
}

func resolveLabelValue(name, value string) string {
	valueReplacements, found := config.Config.Grid.Sorting.CustomValues.Labels[name]
	if found {
		if replacement, ok := valueReplacements[value]; ok {
			return replacement
		}
	}
	return value
}

func getGroupLabel(group *models.APIAlertGroup, label string) string {
	if v := group.Labels.Get(label); v != nil {
		return resolveLabelValue(label, v.Value)
	}
	if v := group.Shared.Labels.Get(label); v != nil {
		return resolveLabelValue(label, v.Value)
	}
	if v := group.Alerts[0].Labels.Get(label); v != nil {
		return resolveLabelValue(label, v.Value)
	}
	return ""
}

func sortByStartsAt(i, j int, groups []models.APIAlertGroup, sortReverse bool) bool {
	if groups[i].LatestStartsAt == groups[j].LatestStartsAt {
		return groups[i].ID > groups[j].ID
	}
	if sortReverse {
		return groups[i].LatestStartsAt.After(groups[j].LatestStartsAt)
	}
	return groups[i].LatestStartsAt.Before(groups[j].LatestStartsAt)
}

func getSortOptions(r models.AlertsRequest) (string, bool, string) {
	sortOrder := r.SortOrder
	if sortOrder == "" {
		sortOrder = config.Config.Grid.Sorting.Order
	}

	sortReverse := r.SortReverse

	sortLabel := r.SortLabel
	if sortLabel == "" {
		sortLabel = config.Config.Grid.Sorting.Label
	}

	return sortOrder, sortReverse, sortLabel
}

func sortAlertGroups(r models.AlertsRequest, groups []models.APIAlertGroup) []models.APIAlertGroup {
	sortOrder, sortReverse, sortLabel := getSortOptions(r)

	switch sortOrder {
	case "startsAt":
		sort.Slice(groups, func(i, j int) bool {
			return sortByStartsAt(i, j, groups, sortReverse)
		})
	case "label":
		sort.Slice(groups, func(i, j int) bool {
			vi := getGroupLabel(&groups[i], sortLabel)
			vj := getGroupLabel(&groups[j], sortLabel)
			if vi == "" && vj == "" {
				// both groups lack this label, fallback to timestamp sort
				return sortByStartsAt(i, j, groups, true)
			}

			if vi == "" {
				// first label is missing
				return sortReverse
			}
			if vj == "" {
				// second label is missing
				return !sortReverse
			}
			if vi == vj {
				// both labels are equal fallback to timestamp sort
				return sortByStartsAt(i, j, groups, true)
			}
			// finnally return groups sorted by label
			if sortReverse {
				return !sortorder.NaturalLess(vi, vj)
			}
			return sortorder.NaturalLess(vi, vj)
		})
	default:
		// sort alert groups so they are always returned in the same order
		// use group ID which is unique and immutable
		sort.Slice(groups, func(i, j int) bool {
			if sortReverse {
				return groups[i].ID < groups[j].ID
			}
			return groups[i].ID > groups[j].ID
		})
	}

	return groups
}

func sortGrids(r models.AlertsRequest, gridLabel string, gridsMap map[string]models.APIGrid, gridSortReverse bool) []models.APIGrid {
	grids := make([]models.APIGrid, 0, len(gridsMap))

	for _, g := range gridsMap {
		g.AlertGroups = sortAlertGroups(r, g.AlertGroups)
		grids = append(grids, g)
	}

	sort.Slice(grids, func(i, j int) bool {
		vi := resolveLabelValue(gridLabel, grids[i].LabelValue)
		vj := resolveLabelValue(gridLabel, grids[j].LabelValue)

		if vi == "" {
			// first label is missing
			return gridSortReverse
		}
		if vj == "" {
			// second label is missing
			return !gridSortReverse
		}
		// finnally return groups sorted by label
		if gridSortReverse {
			return !sortorder.NaturalLess(vi, vj)
		}
		return sortorder.NaturalLess(vi, vj)
	})

	return grids
}

func isPreferredLabel(label, other string) bool {
	ai, aj := -1, -1
	for index, name := range config.Config.Grid.Auto.Order {
		if label == name {
			ai = index
		} else if other == name {
			aj = index
		}
		if ai >= 0 && aj >= 0 {
			return ai < aj
		}
	}
	if ai != aj {
		return aj < ai
	}
	return label < other
}

func autoGridLabel(dedupedAlerts []models.AlertGroup) string {
	alertGroupsCount := len(dedupedAlerts)
	var alertsCount int
	labelToAlertCount := map[string]map[string]int{}
	for _, ag := range dedupedAlerts {
		alertsCount += ag.Alerts.Len()
		for _, alert := range ag.Alerts {
			for _, l := range alert.Labels {
				if _, ok := labelToAlertCount[l.Name]; !ok {
					labelToAlertCount[l.Name] = map[string]int{}
				}
				if _, ok := labelToAlertCount[l.Name][l.Value]; !ok {
					labelToAlertCount[l.Name][l.Value] = 0
				}
				labelToAlertCount[l.Name][l.Value]++
			}
		}
	}
	log.Debug().Int("alerts", alertsCount).Int("groups", alertGroupsCount).Msg("Alerts count for automatic grid label")

	candidates := map[string]int{}
	for key, vals := range labelToAlertCount {
		if slices.StringInSlice(config.Config.Grid.Auto.Ignore, key) {
			continue
		}
		var total int
		uniqueValues := map[string]struct{}{}
		for val, cnt := range vals {
			total += cnt
			uniqueValues[val] = struct{}{}
		}
		log.Debug().Str("label", key).Int("alerts", total).Msg("Number of alerts per label")
		if total < alertsCount {
			continue
		}
		candidates[key] = len(uniqueValues)
	}

	var lastLabel string
	var lastCnt int
	for key, uniqueValues := range candidates {
		if uniqueValues == 1 || uniqueValues >= alertsCount || uniqueValues >= alertGroupsCount {
			log.Debug().Int("variants", uniqueValues).Int("alerts", alertsCount).Int("groups", alertGroupsCount).Str("label", key).Msg("Excluding label from automatic grid selection")
			continue
		}
		log.Debug().Int("variants", uniqueValues).Str("label", key).Msg("Automatic grid label candidate")
		if lastCnt == 0 || uniqueValues < lastCnt || (uniqueValues == lastCnt && isPreferredLabel(key, lastLabel)) {
			lastLabel = key
			lastCnt = uniqueValues
		}
	}
	return lastLabel
}

func filterAlerts(dedupedAlerts []models.AlertGroup, fl []filters.FilterT) (filteredAlerts []models.AlertGroup) {
	var matches int
	for _, ag := range dedupedAlerts {
		agCopy := models.AlertGroup{
			ID:                ag.ID,
			Receiver:          ag.Receiver,
			Labels:            ag.Labels,
			LatestStartsAt:    ag.LatestStartsAt,
			Alerts:            []models.Alert{},
			AlertmanagerCount: map[string]int{},
			StateCount:        map[string]int{},
		}
		for _, s := range models.AlertStateList {
			agCopy.StateCount[s] = 0
		}
		for _, alert := range ag.Alerts {
			var hadMismatch bool
			for _, filter := range fl {
				if filter.GetIsValid() {
					if !filter.Match(&alert, matches) {
						hadMismatch = true
					}
				}
			}
			if len(fl) > 0 && hadMismatch {
				continue
			}

			blockedAMs := map[string]bool{}
			for _, am := range alert.Alertmanager {
				for _, filter := range fl {
					if filter.GetIsValid() && filter.GetIsAlertmanagerFilter() && !filter.MatchAlertmanager(&am) {
						blockedAMs[am.Name] = true
					}
				}
			}
			if len(blockedAMs) > 0 {
				ams := []models.AlertmanagerInstance{}
				for _, am := range alert.Alertmanager {
					_, found := blockedAMs[am.Name]
					if !found {
						ams = append(ams, am)
					}
				}
				alert.Alertmanager = ams
			}
			if len(alert.Alertmanager) == 0 {
				continue
			}

			matches++
			agCopy.Alerts = append(agCopy.Alerts, alert)
			agCopy.StateCount[alert.State]++
		}
		if agCopy.Alerts.Len() > 0 {
			filteredAlerts = append(filteredAlerts, agCopy)
		}
	}

	return filteredAlerts
}

func newStateCount() map[string]int {
	stateCount := map[string]int{}
	for _, s := range models.AlertStateList {
		stateCount[s] = 0
	}
	return stateCount
}

func stateFromStateCount(stateCount map[string]int) string {
	if stateCount[models.AlertStateActive] > 0 {
		return models.AlertStateActive
	}
	if stateCount[models.AlertStateSuppressed] > 0 {
		return models.AlertStateSuppressed
	}
	return models.AlertStateUnprocessed
}
