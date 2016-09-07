#!/bin/bash

killall -9 manor
nohup ./manor > manor.log&
