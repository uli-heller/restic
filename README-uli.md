Meine persönlichen Notizen
==========================

Aktualisierung auf 0.14.0
-------------------------

```
$ OLD_VERSION=0.13.1
$ NEW_VERSION=0.14.0
$ git fetch --all
$ git checkout master
$ git pull
$ git checkout -b "${NEW_VERSION}-uli" "v${NEW_VERSION}"
$ git merge dron-666/cmd-rewrite
$ git merge "${OLD_VERSION}-uli"
  # VERSION und cmd/restic/global.go anpassen
$ git commit -m "Version uli03" .
$ git push -u origin "${NEW_VERSION}-uli"
$ git tag "v${NEW_VERSION}-uli03"
$ git push --tags
```

Anpassungen
-----------

### Dummy-Merge-Strategie

```
$ git config merge.ours.driver true
```

### .gitattributes

```
# Workaround for https://github.com/golang/go/issues/52268.
**/testdata/fuzz/*/* eol=lf
CHANGELOG.md merge=ours
VERSION merge=ours
cmd/restic/global.go merge=ours
```

### VERSION und cmd/restic/global.go anpassen

```diff
diff --git a/VERSION b/VERSION
index a803cc22..11b8f1a3 100644
--- a/VERSION
+++ b/VERSION
@@ -1 +1 @@
-0.14.0
+0.14.0-uli03
diff --git a/cmd/restic/global.go b/cmd/restic/global.go
index 2e7580aa..0c971e68 100644
--- a/cmd/restic/global.go
+++ b/cmd/restic/global.go
@@ -41,7 +41,7 @@ import (
        "golang.org/x/term"
 )
 
-var version = "0.14.0"
+var version = "0.14.0-uli03"
 
 // TimeFormat is the format used for all timestamps printed by restic.
 const TimeFormat = "2006-01-02 15:04:05"
```
