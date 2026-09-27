ARCHS = arm64 arm64e
TARGET := iphone:clang:latest:12.0
THEOS_PACKAGE_SCHEME = roothide
DEBUG = 0
FINALPACKAGE = 1

include $(THEOS)/makefiles/common.mk

TWEAK_NAME = zzzIAPGuard

zzzIAPGuard_FILES = src/Tweak.xm src/IAPGuardConfig.mm src/IAPGuardFailedTransaction.mm src/IAPGuardCancellationRelay.mm
zzzIAPGuard_CFLAGS = -fobjc-arc
ifeq ($(IAPGUARD_DIAGNOSTICS),1)
zzzIAPGuard_CFLAGS += -DIAPGUARD_DIAGNOSTICS=1
endif
zzzIAPGuard_FRAMEWORKS = Foundation StoreKit UIKit
zzzIAPGuard_LIBRARIES = roothide

include $(THEOS_MAKE_PATH)/tweak.mk
