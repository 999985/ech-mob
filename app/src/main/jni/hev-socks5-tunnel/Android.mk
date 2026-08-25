# Copyright (C) 2021 The Android Open Source Project
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

TOP_PATH := $(call my-dir)

ifeq ($(filter $(modules-get-list),yaml),)
    include $(TOP_PATH)/third-part/yaml/Android.mk
endif
ifeq ($(filter $(modules-get-list),lwip),)
    include $(TOP_PATH)/third-part/lwip/Android.mk
endif
ifeq ($(filter $(modules-get-list),hev-task-system),)
    include $(TOP_PATH)/third-part/hev-task-system/Android.mk
endif

LOCAL_PATH = $(TOP_PATH)
SRCDIR := $(LOCAL_PATH)/src

include $(CLEAR_VARS)
include $(LOCAL_PATH)/build.mk
LOCAL_MODULE    := hev-socks5-tunnel
LOCAL_SRC_FILES := $(patsubst $(SRCDIR)/%,src/%,$(SRCFILES))
LOCAL_C_INCLUDES := \
	$(LOCAL_PATH)/src \
	$(LOCAL_PATH)/src/misc \
	$(LOCAL_PATH)/src/core/src \
	$(LOCAL_PATH)/src/core/include \
	$(LOCAL_PATH)/third-part/yaml/src \
	$(LOCAL_PATH)/third-part/yaml/include \
	$(LOCAL_PATH)/third-part/lwip/src/include \
	$(LOCAL_PATH)/third-part/lwip/src/ports/include \
	$(LOCAL_PATH)/third-part/hev-task-system/include \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/aide \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/core \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/io \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/itc \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/sync \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/task \
	$(LOCAL_PATH)/third-part/hev-task-system/src/kern/time \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/cio/base \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/cio/buffer \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/cio/fd \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/cio/null \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/cio/socket \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/dns \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/io/basic \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/io/buffer \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/io/pipe \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/io/poll \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/io/socket \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/list \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/misc \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/object \
	$(LOCAL_PATH)/third-part/hev-task-system/src/lib/rbtree \
	$(LOCAL_PATH)/third-part/hev-task-system/src/mem/api \
	$(LOCAL_PATH)/third-part/hev-task-system/src/mem/base \
	$(LOCAL_PATH)/third-part/hev-task-system/src/mem/simple \
	$(LOCAL_PATH)/third-part/hev-task-system/src/mem/slice
LOCAL_CFLAGS += -DFD_SET_DEFINED -DSOCKLEN_T_DEFINED -DENABLE_LIBRARY
LOCAL_CFLAGS += $(VERSION_CFLAGS)
ifeq ($(TARGET_ARCH_ABI),armeabi-v7a)
LOCAL_CFLAGS += -mfpu=neon
endif
LOCAL_STATIC_LIBRARIES := yaml lwip hev-task-system
LOCAL_LDFLAGS += -Wl,-z,max-page-size=16384
LOCAL_LDFLAGS += -Wl,-z,common-page-size=16384
include $(BUILD_SHARED_LIBRARY)
