#!/bin/bash

killall -9 rancher
nohup ./rancher > rancher.log&
