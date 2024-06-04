#!/bin/bash

wd=$(mktemp -d)
cd fixtures
cp -r .taubyte "${wd}"
cp go.mod.tmpl "${wd}/go.mod"
cd -
tar -czvf fixtures.tar -C ${wd} .
rm -fr "${wd}"