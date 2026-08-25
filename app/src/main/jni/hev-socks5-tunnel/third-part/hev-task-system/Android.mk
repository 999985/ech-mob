# Copyright (C) 2018 The Android Open Source Project
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

LOCAL_PATH := $(call my-dir)
SRCDIR := $(LOCAL_PATH)/src

include $(CLEAR_VARS)
include $(LOCAL_PATH)/build.mk
include $(LOCAL_PATH)/configs.mk
LOCAL_MODULE    := libhev-task-system
LOCAL_SRC_FILES := $(patsubst $(SRCDIR)/%,src/%,$(SRCFILES))
LOCAL_C_INCLUDES := \
	$(LOCAL_PATH)/src \
	$(LOCAL_PATH)/include \
	$(LOCAL_PATH)/src/kern/aide \
	$(LOCAL_PATH)/src/kern/core \
	$(LOCAL_PATH)/src/kern/io \
	$(LOCAL_PATH)/src/kern/itc \
	$(LOCAL_PATH)/src/kern/sync \
	$(LOCAL_PATH)/src/kern/task \
	$(LOCAL_PATH)/src/kern/time \
	$(LOCAL_PATH)/src/lib/cio/base \
	$(LOCAL_PATH)/src/lib/cio/buffer \
	$(LOCAL_PATH)/src/lib/cio/fd \
	$(LOCAL_PATH)/src/lib/cio/null \
	$(LOCAL_PATH)/src/lib/cio/socket \
	$(LOCAL_PATH)/src/lib/dns \
	$(LOCAL_PATH)/src/lib/io/basic \
	$(LOCAL_PATH)/src/lib/io/buffer \
	$(LOCAL_PATH)/src/lib/io/pipe \
	$(LOCAL_PATH)/src/lib/io/poll \
	$(LOCAL_PATH)/src/lib/io/socket \
	$(LOCAL_PATH)/src/lib/list \
	$(LOCAL_PATH)/src/lib/misc \
	$(LOCAL_PATH)/src/lib/object \
	$(LOCAL_PATH)/src/lib/rbtree \
	$(LOCAL_PATH)/src/mem/api \
	$(LOCAL_PATH)/src/mem/base \
	$(LOCAL_PATH)/src/mem/simple \
	$(LOCAL_PATH)/src/mem/slice
LOCAL_CFLAGS += -fvisibility=hidden $(CONFIG_CFLAGS)
ifeq ($(TARGET_ARCH_ABI),armeabi-v7a)
LOCAL_CFLAGS += -mfpu=neon
endif
include $(BUILD_STATIC_LIBRARY)
