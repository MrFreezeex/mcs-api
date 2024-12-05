/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package conformance

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("", Label(OptionalLabel, DNSLabel, ClusterIPLabel), func() {
	t := newTestDriver()

	JustBeforeEach(func() {
		t.createServiceExport(&clients[0])
	})

	Specify("A DNS lookup of the <service>.<ns>.svc.clusterset.local domain for a ClusterIP service should resolve to the "+
		"clusterset IP", func() {
		AddReportEntry(SpecRefReportEntry, "https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api#dns")

		By("Retrieving ServiceImport")

		serviceImport := t.awaitServiceImport(&clients[0], t.helloService.Name, func(serviceImport *v1alpha1.ServiceImport) bool {
			return len(serviceImport.Spec.IPs) > 0
		})

		Expect(serviceImport).NotTo(BeNil(), "ServiceImport was not found")
		Expect(serviceImport.Spec.IPs).ToNot(BeEmpty(), "ServiceImport does not contain an IP")

		clusterSetIP := serviceImport.Spec.IPs[0]

		By(fmt.Sprintf("Found ServiceImport with clusterset IP %q", clusterSetIP))

		command := []string{"sh", "-c", fmt.Sprintf("nslookup %s.%s.svc.clusterset.local", t.helloService.Name, t.namespace)}

		for _, client := range clients {
			By(fmt.Sprintf("Executing command %q on cluster %q", strings.Join(command, " "), client.name))

			Eventually(func() string {
				stdout, _, _ := execCmd(client.k8s, client.rest, t.requestPod.Name, t.namespace, command)
				return string(stdout)
			}, 20, 1).Should(ContainSubstring(clusterSetIP), reportNonConformant(""))
		}
	})

	Specify("A DNS SRV query of the <service>.<ns>.svc.clusterset.local domain for a ClusterIP service should return valid SRV "+
		"records", func() {
		AddReportEntry(SpecRefReportEntry, "https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api#dns")

		domainName := fmt.Sprintf("%s.%s.svc.clusterset.local", t.helloService.Name, t.namespace)

		for _, client := range clients {
			srvRecs := t.expectSRVRecords(&client, domainName)

			expSRVRecs := []SRVRecord{{
				port:       t.helloService.Spec.Ports[0].Port,
				domainName: domainName,
			}}

			Expect(srvRecs).To(Equal(expSRVRecs), reportNonConformant(
				fmt.Sprintf("Received SRV records %v do not match the expected records %v", srvRecs, expSRVRecs)))
		}
	})

	Specify("DNS lookups of the <service>.<ns>.svc.cluster.local domain for a ClusterIP service should only resolve "+
		"local services", func() {
		AddReportEntry(SpecRefReportEntry, "https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api#dns")

		By(fmt.Sprintf("Retrieving local Service on cluster %q", clients[0].name))

		var resolvedIP string

		Eventually(func() string {
			svc, err := clients[0].k8s.CoreV1().Services(t.namespace).Get(context.TODO(), t.helloService.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Error retrieving the local Service")

			resolvedIP = svc.Spec.ClusterIP

			return resolvedIP
		}, 20, 1).ShouldNot(BeEmpty(), "The service was not assigned a cluster IP")

		By(fmt.Sprintf("Found local Service cluster IP %q", resolvedIP))

		command := []string{"sh", "-c", fmt.Sprintf("nslookup %s.%s.svc.cluster.local", t.helloService.Name, t.namespace)}

		By(fmt.Sprintf("Executing command %q on cluster %q", strings.Join(command, " "), clients[0].name))

		Eventually(func() string {
			stdout, _, _ := execCmd(clients[0].k8s, clients[0].rest, t.requestPod.Name, t.namespace, command)
			return string(stdout)
		}, 20, 1).Should(ContainSubstring(resolvedIP), reportNonConformant(""))
	})
})

func (t *testDriver) expectSRVRecords(c *clusterClients, domainName string) []SRVRecord {
	command := []string{"sh", "-c", "nslookup -type=SRV " + domainName}

	By(fmt.Sprintf("Executing command %q on cluster %q", strings.Join(command, " "), c.name))

	var srvRecs []SRVRecord

	Eventually(func() []SRVRecord {
		stdout, _, _ := execCmd(c.k8s, c.rest, t.requestPod.Name, t.namespace, command)
		srvRecs = parseSRVRecords(string(stdout))

		return srvRecs
	}, 20, 1).ShouldNot(BeEmpty(), reportNonConformant(""))

	return srvRecs
}

// Match SRV records from nslookup of the form:
//
//	hello.mcs-conformance-1686874467.svc.clusterset.local	service = 0 50 42 hello.mcs-conformance-1686874467.svc.clusterset.local
//
// to extract the port and target domain name (the last two tokens)
var srvRecordRegEx = regexp.MustCompile(`.*=\s*\d*\s*\d*\s*(\d*)\s*([a-zA-Z0-9-.]*)`)

type SRVRecord struct {
	port       int32
	domainName string
}

func parseSRVRecords(str string) []SRVRecord {
	var recs []SRVRecord

	matches := srvRecordRegEx.FindAllStringSubmatch(str, -1)
	for i := range matches {
		// First match at index 0 is the full text that was matched; index 1 is the port and index 2 is the domain name.
		port, _ := strconv.ParseInt(matches[i][1], 10, 32)
		recs = append(recs, SRVRecord{
			port:       int32(port),
			domainName: matches[i][2],
		})
	}

	return recs
}
