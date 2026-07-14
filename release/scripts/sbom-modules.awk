BEGIN { n = 0 }
NF >= 2 && $1 != "github.com/orvix/orvix" {
  n++
  id = $1
  gsub(/[^A-Za-z0-9.-]/, "-", id)
  print ""
  print "PackageName: " $1
  print "SPDXID: SPDXRef-Dependency-" id "-" n
  print "PackageVersion: " $2
  print "PackageSupplier: NOASSERTION"
  print "PackageDownloadLocation: NOASSERTION"
  print "FilesAnalyzed: false"
  print "PackageLicenseConcluded: NOASSERTION"
  print "PackageLicenseDeclared: NOASSERTION"
  print "PackageCopyrightText: NOASSERTION"
  print "ExternalRef: PACKAGE-MANAGER purl pkg:golang/" $1 "@" $2
  print "Relationship: SPDXRef-Package-Orvix DEPENDS_ON SPDXRef-Dependency-" id "-" n
}
