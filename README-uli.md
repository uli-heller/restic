Meine persönlichen Notizen
==========================

Aktualisierung auf 0.14.0
-------------------------

```
$ NEW_VERSION=0.14.0
$ git fetch --all
$ git checkout master
$ git pull
$ git checkout -b "${NEW_VERSION}-uli" "v${NEW_VERSION}"
$ git merge dron-666/cmd-rewrite
  # VERSION und cmd/restic/global.go anpassen
$ git commit -m "Version uli03" .
$ git push -u origin 0.14.0-uli
$ git tag v0.14.0-uli03
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
