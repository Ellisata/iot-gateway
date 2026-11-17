<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与设备对象/协议页同一套字号与留白层级 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ t('menu.thingModel') }}</h1>
        <p class="iot-sub">{{ t('thingModel.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('thingModel.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.keyword"
            class="iot-search"
            :placeholder="t('thingModel.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-select
            v-model="queryForm.category"
            class="thing-category-select"
            :placeholder="t('thingModel.categoryFilter')"
            clearable
            @change="handleSearch"
          >
            <el-option
              v-for="opt in filterCategoryOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <el-button :icon="Download" @click="handleModelTemplateDownload">
            {{ t('common.downloadTemplate') }}
          </el-button>
          <el-button :icon="Upload" :loading="modelImporting" @click="handleModelImport">
            {{ t('common.import') }}
          </el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">
            {{ t('thingModel.add') }}
          </el-button>
          <input
            ref="modelFileInputRef"
            type="file"
            accept=".xlsx"
            class="hidden"
            @change="handleModelFileChange"
          />
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        row-key="id"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <!-- code 是模型的稳定身份，用等宽字体；名称才是给人看的 -->
        <el-table-column :label="t('thingModel.code')" min-width="180">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Box /></el-icon>
              </span>
              <span class="iot-mono" :title="row.code">{{ row.code }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.name')" min-width="180">
          <template #default="{ row }">
            <span class="thing-name" :title="row.name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thingModel.categoryLabel')" width="128" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.category ? 'primary' : 'info'">
              {{ categoryLabel(row.category) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('thingModel.version')"
          width="96"
          align="center"
          class-name="iot-col-num"
        >
          <template #default="{ row }">{{ row.version || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('thingModel.propertyCount')" width="96" align="center">
          <template #default="{ row }">
            <el-button link type="primary" @click="openProperties(row)">
              {{ row.propertyCount }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column :label="t('thingModel.deviceCount')" width="90" align="center">
          <template #default="{ row }">
            <el-button
              v-if="row.deviceCount > 0"
              link
              type="primary"
              @click="openDevices(row)"
            >
              {{ row.deviceCount }}
            </el-button>
            <span v-else class="iot-cell-muted">0</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="updatedAt"
          :label="t('common.updatedAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="168" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('thingModel.manageProperties')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="ListDetails"
                  :aria-label="t('thingModel.manageProperties')"
                  @click="openProperties(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('common.edit')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="EditPen"
                  :aria-label="t('common.edit')"
                  @click="handleEdit(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('common.delete')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="danger"
                  link
                  size="small"
                  :icon="Delete"
                  :aria-label="t('common.delete')"
                  @click="handleDelete(row)"
                />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ hasQuery
                ? t('thingModel.emptySearch')
                : t('thingModel.emptyTitle') }}
            </p>
            <p v-if="!hasQuery" class="iot-empty-hint">{{ t('thingModel.emptyHint') }}</p>
            <el-button v-if="hasQuery" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('thingModel.add') }}
            </el-button>
          </div>
        </template>
      </el-table>

      <div class="iot-pager">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </section>

    <!-- ========== 物模型表单 ========== -->
    <el-dialog
      v-model="modelDialogVisible"
      class="iot-dialog"
      :title="isModelEdit ? t('thingModel.editTitle') : t('thingModel.addTitle')"
      width="640px"
      :close-on-click-modal="false"
      @closed="handleModelDialogClosed"
    >
      <el-form
        ref="modelFormRef"
        :model="modelForm"
        :rules="modelRules"
        label-width="108px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('thingModel.code')" prop="code">
          <el-input
            v-model="modelForm.code"
            :disabled="isModelEdit"
            :placeholder="t('thingModel.codePlaceholder')"
            maxlength="64"
          />
          <div class="iot-form-hint is-block">
            {{ isModelEdit ? t('thingModel.codeImmutableHint') : t('thingModel.codeHint') }}
          </div>
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="modelForm.name"
            :placeholder="t('thingModel.namePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.categoryLabel')" prop="category">
          <el-select v-model="modelForm.category" style="width: 100%">
            <el-option
              v-for="opt in formCategoryOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('thingModel.version')" prop="version">
          <el-input
            v-model="modelForm.version"
            :placeholder="t('thingModel.versionPlaceholder')"
            maxlength="32"
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.bindDevices')" prop="deviceIds">
          <!-- 设备可能上千台：select-v2 的虚拟滚动只渲染可视行，标签折叠到 3 个，
               避免一次性铺出整张设备表。候选行用自定义布局，状态用标签表达，
               标签本身只留「名称 (编码)」，不再把长句带进输入框。 -->
          <el-select-v2
            v-model="modelForm.deviceIds"
            :options="modelDeviceSelectOptions"
            multiple
            filterable
            remote
            :remote-method="handleDeviceSearch"
            :debounce="250"
            collapse-tags
            collapse-tags-tooltip
            :max-collapse-tags="3"
            :item-height="38"
            :height="320"
            :loading="modelDeviceOptionsLoading"
            :placeholder="t('thingModel.bindDevicesPlaceholder')"
            :no-data-text="t('thingModel.bindDevicesEmpty')"
            :no-match-text="t('thingModel.bindDevicesNoMatch')"
            style="width: 100%"
          >
            <template #default="{ item }">
              <div class="thing-device-option">
                <!-- 名称与编码作为一个整体靠左；状态标签靠右，中间留白分隔，
                     避免编码看起来像是挂在模型名旁边的附属信息 -->
                <span class="thing-device-option-main">
                  <span class="thing-device-option-name" :title="item.name">{{ item.name }}</span>
                  <span class="thing-device-option-code" :title="item.code">{{ item.code }}</span>
                </span>
                <span class="thing-device-option-tags">
                  <el-tag
                    v-if="modelForm.id && item.modelId === modelForm.id"
                    size="small"
                    effect="plain"
                    class="thing-device-option-tag"
                  >
                    {{ t('thingModel.deviceCurrentModel') }}
                  </el-tag>
                  <el-tag
                    v-else-if="item.modelId"
                    size="small"
                    type="info"
                    effect="plain"
                    class="thing-device-option-tag thing-device-option-model"
                    :title="item.modelName"
                  >
                    {{ item.modelName || item.modelId }}
                  </el-tag>
                  <el-tag
                    v-if="item.locked"
                    size="small"
                    type="warning"
                    effect="plain"
                    class="thing-device-option-tag"
                  >
                    {{ t('thingModel.deviceLockedShort') }}
                  </el-tag>
                  <el-tag
                    v-if="item.status === 0"
                    size="small"
                    type="info"
                    effect="plain"
                    class="thing-device-option-tag"
                  >
                    {{ t('common.enableDisable.off') }}
                  </el-tag>
                </span>
              </div>
            </template>
            <template #footer>
              <div v-if="modelDeviceOptionsTruncated" class="thing-device-option-footer">
                {{ t('thingModel.bindDevicesTruncated') }}
              </div>
            </template>
          </el-select-v2>
          <div class="iot-form-hint is-block">{{ t('thingModel.bindDevicesHint') }}</div>
          <div
            v-if="!modelDeviceOptionsLoading && !modelDeviceOptionsReady"
            class="iot-form-hint is-block thing-hint-warning"
          >
            {{ t('thingModel.deviceBindingLoadFailed') }}
          </div>
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="modelForm.description"
            type="textarea"
            :rows="3"
            :placeholder="t('thingModel.descriptionPlaceholder')"
            maxlength="500"
            show-word-limit
          />
        </el-form-item>
        <el-form-item v-if="isModelEdit" :label="t('common.status')" prop="status">
          <el-switch
            v-model="modelForm.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="modelDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="modelSubmitting" @click="handleSubmitModel">
          {{ t('common.save') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- ========== 属性管理抽屉 ========== -->
    <el-drawer
      v-model="propertyDrawerVisible"
      class="thing-property-drawer"
      size="min(920px, 96vw)"
      :title="propertyDrawerTitle"
      @closed="handleDrawerClosed"
    >
      <div class="thing-drawer-body">
        <div class="thing-drawer-head">
          <p class="thing-drawer-sub">{{ t('thingModel.propertiesSubtitle') }}</p>
          <div class="thing-drawer-actions">
            <el-button :icon="Download" @click="handlePropertyTemplateDownload">
              {{ t('common.downloadTemplate') }}
            </el-button>
            <el-button :icon="Upload" :loading="propertyImporting" @click="handlePropertyImport">
              {{ t('common.import') }}
            </el-button>
            <el-button type="primary" :icon="Plus" @click="handleAddProperty">
              {{ t('thingModel.addProperty') }}
            </el-button>
            <input
              ref="propertyFileInputRef"
              type="file"
              accept=".xlsx"
              class="hidden"
              @change="handlePropertyFileChange"
            />
          </div>
        </div>

        <el-table
          class="iot-table is-compact"
          :data="properties"
          v-loading="propertyLoading"
          row-key="id"
        >
          <el-table-column
            :label="t('thingModel.sortOrder')"
            width="72"
            align="center"
            class-name="iot-col-num"
          >
            <template #default="{ row }">{{ row.sortOrder }}</template>
          </el-table-column>
          <el-table-column :label="t('thingModel.identifier')" min-width="170">
            <template #default="{ row }">
              <span class="iot-mono" :title="row.identifier">{{ row.identifier }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.name')" min-width="140" show-overflow-tooltip>
            <template #default="{ row }">{{ row.name }}</template>
          </el-table-column>
          <el-table-column :label="t('thingModel.kind.label')" width="116" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain">{{ kindLabel(row.kind) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.access.label')" width="104" align="center">
            <template #default="{ row }">
              <el-tag
                size="small"
                effect="plain"
                :type="row.accessMode === 'R' ? 'info' : 'warning'"
              >
                {{ accessLabel(row.accessMode) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.pointCount')" width="90" align="center">
            <template #default="{ row }">
              <el-button
                v-if="row.pointCount > 0"
                link
                type="primary"
                @click="openPropertyPoints(row)"
              >
                {{ row.pointCount }}
              </el-button>
              <span v-else class="iot-cell-muted">0</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.unit')" width="86" align="center">
            <template #default="{ row }">
              <span :class="row.unit ? '' : 'iot-cell-muted'">{{ row.unit || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.status')" width="90" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
                {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.action')" width="100" align="center" fixed="right">
            <template #default="{ row }">
              <div class="iot-row-actions">
                <el-tooltip :content="t('common.edit')" placement="top">
                  <el-button
                    class="iot-row-btn"
                    type="primary"
                    link
                    size="small"
                    :icon="EditPen"
                    :aria-label="t('common.edit')"
                    @click="handleEditProperty(row)"
                  />
                </el-tooltip>
                <el-tooltip :content="t('common.delete')" placement="top">
                  <el-button
                    class="iot-row-btn"
                    type="danger"
                    link
                    size="small"
                    :icon="Delete"
                    :aria-label="t('common.delete')"
                    @click="handleDeleteProperty(row)"
                  />
                </el-tooltip>
              </div>
            </template>
          </el-table-column>

          <template #empty>
            <div class="iot-empty">
              <span class="iot-empty-mark" aria-hidden="true">
                <el-icon><ListDetails /></el-icon>
              </span>
              <p class="iot-empty-title">{{ t('thingModel.propertyEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.propertyEmptyHint') }}</p>
              <el-button type="primary" :icon="Plus" @click="handleAddProperty">
                {{ t('thingModel.addProperty') }}
              </el-button>
            </div>
          </template>
        </el-table>

        <div v-if="propertyTotal > 0" class="iot-pager">
          <el-pagination
            v-model:current-page="propertyPage"
            v-model:page-size="propertyPageSize"
            :page-sizes="[10, 20, 50, 100]"
            :total="propertyTotal"
            layout="total, sizes, prev, pager, next"
            background
            small
            @size-change="handlePropertySizeChange"
            @current-change="handlePropertyCurrentChange"
          />
        </div>
      </div>
    </el-drawer>

    <!-- ========== 属性绑定点位抽屉 ========== -->
    <el-drawer
      v-model="propertyPointsDrawerVisible"
      class="thing-property-points-drawer"
      size="min(820px, 96vw)"
      :title="propertyPointsTitle"
      @closed="handlePropertyPointsClosed"
    >
      <div class="thing-drawer-body">
        <div class="thing-drawer-head">
          <p class="thing-drawer-sub">{{ t('thingModel.propertyPointsSubtitle') }}</p>
        </div>
        <el-table
          class="iot-table is-compact"
          :data="propertyPoints"
          v-loading="propertyPointsLoading"
          row-key="addressId"
          max-height="60vh"
        >
          <el-table-column :label="t('thingModel.pointDevice')" min-width="180">
            <template #default="{ row }">
              <div class="thing-point-device">
                <span class="thing-point-device-name" :title="row.deviceName">
                  {{ row.deviceName }}
                </span>
                <span class="thing-point-device-code" :title="row.deviceCode">
                  {{ row.deviceCode }}
                </span>
              </div>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.pointAddress')" min-width="130">
            <template #default="{ row }">
              <span class="iot-mono" :title="row.name">{{ row.name }}</span>
            </template>
          </el-table-column>
          <el-table-column
            :label="t('thingModel.pointLabel')"
            min-width="140"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{ row.label || '—' }}</template>
          </el-table-column>
          <el-table-column :label="t('thingModel.pointDataType')" width="120" align="center">
            <template #default="{ row }">
              <span :title="row.dataType">{{ row.commonDataType || row.dataType }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.access.label')" width="96" align="center">
            <template #default="{ row }">
              <el-tag
                size="small"
                effect="plain"
                :type="row.rwPermission === 'R' ? 'info' : 'warning'"
              >
                {{ accessLabel(row.rwPermission) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.status')" width="90" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
                {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
              </el-tag>
            </template>
          </el-table-column>
          <template #empty>
            <div class="iot-empty">
              <span class="iot-empty-mark" aria-hidden="true">
                <el-icon><ListDetails /></el-icon>
              </span>
              <p class="iot-empty-title">{{ t('thingModel.propertyPointsEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.propertyPointsEmptyHint') }}</p>
            </div>
          </template>
        </el-table>
      </div>
    </el-drawer>

    <!-- ========== 已绑定设备抽屉 ========== -->
    <el-drawer
      v-model="deviceDrawerVisible"
      class="thing-device-drawer"
      size="min(760px, 96vw)"
      :title="deviceDrawerTitle"
      @closed="handleDeviceDrawerClosed"
    >
      <div class="thing-drawer-body">
        <div class="thing-drawer-head">
          <p class="thing-drawer-sub">{{ t('thingModel.devicesSubtitle') }}</p>
        </div>
        <el-table
          class="iot-table is-compact"
          :data="boundDevices"
          v-loading="boundDevicesLoading"
          row-key="id"
          max-height="60vh"
        >
          <el-table-column :label="t('deviceObject.code')" min-width="160">
            <template #default="{ row }">
              <span class="iot-mono" :title="row.code">{{ row.code }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.name')" min-width="160" show-overflow-tooltip>
            <template #default="{ row }">{{ row.name }}</template>
          </el-table-column>
          <el-table-column
            :label="t('deviceObject.protocol')"
            min-width="150"
            show-overflow-tooltip
          >
            <template #default="{ row }">
              <span :class="row.protocolName ? '' : 'iot-cell-muted'">
                {{ row.protocolName || '—' }}
              </span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.status')" width="90" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
                {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
              </el-tag>
            </template>
          </el-table-column>
          <template #empty>
            <div class="iot-empty">
              <span class="iot-empty-mark" aria-hidden="true">
                <el-icon><Box /></el-icon>
              </span>
              <p class="iot-empty-title">{{ t('thingModel.devicesEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.devicesEmptyHint') }}</p>
            </div>
          </template>
        </el-table>
      </div>
    </el-drawer>

    <!-- ========== 属性表单 ========== -->
    <el-dialog
      v-model="propertyDialogVisible"
      class="iot-dialog"
      :title="isPropertyEdit ? t('thingModel.editPropertyTitle') : t('thingModel.addPropertyTitle')"
      width="640px"
      append-to-body
      :close-on-click-modal="false"
      @closed="handlePropertyDialogClosed"
    >
      <el-form
        ref="propertyFormRef"
        :model="propertyForm"
        :rules="propertyRules"
        label-width="108px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('thingModel.identifier')" prop="identifier">
          <el-input
            v-model="propertyForm.identifier"
            :disabled="isPropertyEdit"
            :placeholder="t('thingModel.identifierPlaceholder')"
            maxlength="64"
          />
          <div class="iot-form-hint is-block">
            {{
              isPropertyEdit
                ? t('thingModel.identifierImmutableHint')
                : t('thingModel.identifierHint')
            }}
          </div>
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="propertyForm.name"
            :placeholder="t('thingModel.propertyNamePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.kind.label')" prop="kind">
          <el-select v-model="propertyForm.kind" style="width: 100%">
            <el-option
              v-for="item in kindOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('thingModel.access.label')" prop="accessMode">
          <el-select v-model="propertyForm.accessMode" style="width: 100%">
            <el-option
              v-for="item in accessOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('thingModel.bindPoints')" prop="addressIds">
          <el-select
            v-model="propertyForm.addressIds"
            multiple
            filterable
            collapse-tags
            collapse-tags-tooltip
            :loading="propertyPointOptionsLoading"
            :placeholder="t('thingModel.bindPointsPlaceholder')"
            :no-data-text="t('thingModel.bindPointsEmpty')"
            style="width: 100%"
          >
            <el-option-group
              v-for="g in propertyPointGroups"
              :key="g.deviceId"
              :label="g.deviceLabel"
            >
              <el-option
                v-for="p in g.points"
                :key="p.addressId"
                :label="pointOptionLabel(p)"
                :value="p.addressId"
                :disabled="pointOptionDisabled(p)"
              />
            </el-option-group>
          </el-select>
          <div class="iot-form-hint is-block">{{ t('thingModel.bindPointsHint') }}</div>
          <div
            v-if="!propertyPointOptionsLoading && !propertyPointOptionsReady"
            class="iot-form-hint is-block thing-hint-warning"
          >
            {{ t('thingModel.pointBindingLoadFailed') }}
          </div>
        </el-form-item>
        <el-form-item :label="t('thingModel.unit')" prop="unit">
          <el-input
            v-model="propertyForm.unit"
            :placeholder="t('thingModel.unitPlaceholder')"
            maxlength="32"
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.sortOrder')" prop="sortOrder">
          <el-input-number v-model="propertyForm.sortOrder" :min="0" :max="99999" />
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="propertyForm.description"
            type="textarea"
            :rows="2"
            :placeholder="t('thingModel.descriptionPlaceholder')"
            maxlength="500"
            show-word-limit
          />
        </el-form-item>
        <el-form-item v-if="isPropertyEdit" :label="t('common.status')" prop="status">
          <el-switch
            v-model="propertyForm.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="propertyDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="propertySubmitting" @click="handleSubmitProperty">
          {{ t('common.save') }}
        </el-button>
      </template>
    </el-dialog>

    <ImportResultDialog v-model:visible="modelImportResultVisible" :result="modelImportResult" />
    <ImportResultDialog
      v-model:visible="propertyImportResultVisible"
      :result="propertyImportResult"
    />
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconBox as Box,
  IconDownload as Download,
  IconEdit as EditPen,
  IconListDetails as ListDetails,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  createThingModel,
  createThingModelProperty,
  deleteThingModel,
  deleteThingModelProperty,
  downloadThingModelPropertyTemplate,
  downloadThingModelTemplate,
  getThingModelList,
  importThingModelProperties,
  importThingModels,
  listThingModelDeviceOptions,
  listThingModelDevices,
  listThingModelPointOptions,
  listThingModelPropertyPoints,
  pageThingModelProperties,
  updateThingModel,
  updateThingModelProperty,
} from '@/api/modules/thingModel'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'

const { t, te } = useI18n()

// 内置分类的展示名来自 i18n；开放接口下发的平台自定义分类没有内置文案，
// 直接展示 code（平台是分类词表的权威源，网关不跟随发版）。
const CATEGORY_CODES = ['sensor', 'meter', 'actuator', 'robot', 'plc', 'other']
const KIND_CODES = ['bool', 'int', 'uint', 'float', 'string', 'time']
const ACCESS_CODES = ['R', 'W', 'RW']

const categoryLabel = (code) => {
  if (!code) return t('thingModel.category.none')
  const key = `thingModel.category.${code}`
  return te(key) ? t(key) : code
}
const kindLabel = (kind) => t(`thingModel.kind.${kind}`)
const accessLabel = (mode) => t(`thingModel.access.${mode}`)

const filterCategoryOptions = computed(() => {
  const custom = [...new Set(tableData.value.map((r) => r.category).filter((code) => code && !CATEGORY_CODES.includes(code)))]
  return [...CATEGORY_CODES, ...custom].map((code) => ({ value: code, label: categoryLabel(code) }))
})
// 表单里的“未分类”是空串，和筛选框的“全部”不同：筛选空串 = 不过滤。
const formCategoryOptions = computed(() => {
  // 本地管理端只允许新建内置分类；编辑时保留开放接口已下发的自定义分类。
  const options = [
    { value: '', label: t('thingModel.category.none') },
    ...CATEGORY_CODES.map((code) => ({ value: code, label: categoryLabel(code) })),
  ]
  const current = modelForm.category
  if (current && !options.some((o) => o.value === current)) {
    options.push({ value: current, label: categoryLabel(current) })
  }
  return options
})
const kindOptions = computed(() =>
  KIND_CODES.map((code) => ({ value: code, label: kindLabel(code) })),
)
const accessOptions = computed(() =>
  ACCESS_CODES.map((code) => ({ value: code, label: accessLabel(code) })),
)

// ---------- 列表 ----------
const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const queryForm = reactive({ keyword: '', category: '' })

const hasQuery = computed(() => !!(queryForm.keyword || queryForm.category))
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

async function fetchList() {
  loading.value = true
  try {
    const res = await getThingModelList({
      page: currentPage.value,
      size: pageSize.value,
      keyword: queryForm.keyword,
      category: queryForm.category,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 错误提示由 axios 拦截器统一给出
    tableData.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.keyword = ''
  queryForm.category = ''
  currentPage.value = 1
  fetchList()
}

function handleSizeChange(size) {
  pageSize.value = size
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange(page) {
  currentPage.value = page
  fetchList()
}

// ---------- 物模型表单 ----------
const modelDialogVisible = ref(false)
const modelSubmitting = ref(false)
const modelFormRef = ref(null)
const modelForm = reactive({
  id: '',
  code: '',
  name: '',
  category: '',
  version: '1.0.0',
  description: '',
  status: 1,
  deviceIds: [],
})

// ---------- 设备绑定候选（远程搜索） ----------
// 设备可能上万台，不能一次全拉：初始只取前 200 条，输入关键字时走 remote-method。
// 已选设备单独缓存并在选项里并回，否则搜索词一变，标签就只剩 id。
// 选项加载失败时编辑保存不发送 deviceIds —— 否则会把「没加载出来」误当成「全部解绑」。
const modelDeviceOptions = ref([])
const modelDeviceOptionsLoading = ref(false)
const modelDeviceOptionsReady = ref(false)
const modelDeviceOptionsTruncated = ref(false)
const originalModelDeviceIDs = ref([])
const deviceOptionCache = new Map()
let modelOptionsToken = 0
// 首次加载还没回来就有人输入搜索词：先排队，等回填完再发搜索请求，
// 否则 prefill 请求会被 token 作废，编辑态的已选设备就丢了。
let pendingDeviceSearch = null

async function fetchModelDeviceOptions({ modelId = '', keyword = '', prefill = false } = {}) {
  const token = ++modelOptionsToken
  modelDeviceOptionsLoading.value = true
  if (prefill) {
    modelDeviceOptionsReady.value = false
    modelDeviceOptions.value = []
    modelDeviceOptionsTruncated.value = false
  }
  try {
    const res = await listThingModelDeviceOptions({ modelId, keyword })
    if (token !== modelOptionsToken) return
    const rows = res?.records ?? []
    for (const d of rows) deviceOptionCache.set(d.id, d)
    modelDeviceOptions.value = rows
    modelDeviceOptionsTruncated.value = !!res?.truncated
    modelDeviceOptionsReady.value = true
    if (prefill) {
      const bound = modelId ? rows.filter((d) => d.modelId === modelId).map((d) => d.id) : []
      originalModelDeviceIDs.value = bound
      modelForm.deviceIds = [...bound]
      if (pendingDeviceSearch !== null) {
        const queued = pendingDeviceSearch
        pendingDeviceSearch = null
        fetchModelDeviceOptions({ modelId, keyword: queued })
      }
    }
  } catch {
    // 搜索失败保留上一批结果；只有首次加载失败才清空并阻止提交绑定字段
    if (token === modelOptionsToken && prefill) {
      modelDeviceOptions.value = []
      modelDeviceOptionsReady.value = false
      modelDeviceOptionsTruncated.value = false
    }
  } finally {
    if (token === modelOptionsToken) modelDeviceOptionsLoading.value = false
  }
}

function handleDeviceSearch(keyword) {
  if (!modelDeviceOptionsReady.value) {
    pendingDeviceSearch = keyword || ''
    // 首次加载失败时借一次输入重试；成功后自动补发这次搜索。
    // 正在加载则不重复发，等它回来再处理队列。
    if (!modelDeviceOptionsLoading.value) {
      fetchModelDeviceOptions({ modelId: modelForm.id, prefill: true })
    }
    return
  }
  fetchModelDeviceOptions({ modelId: modelForm.id, keyword: keyword || '' })
}

function resetModelDeviceOptions() {
  modelOptionsToken += 1
  pendingDeviceSearch = null
  deviceOptionCache.clear()
  modelDeviceOptions.value = []
  modelDeviceOptionsLoading.value = false
  modelDeviceOptionsReady.value = false
  modelDeviceOptionsTruncated.value = false
  originalModelDeviceIDs.value = []
}

function openModelDeviceOptions(modelId) {
  resetModelDeviceOptions()
  fetchModelDeviceOptions({ modelId: modelId || '', prefill: true })
}

// 锁定在别的模型的设备不能直接搬走；锁定在当前模型的保持可选（并允许查看），
// 但取消勾选会在提交前被拦下。
function modelDeviceOptionDisabled(d) {
  return !!d.locked && !!d.modelId && d.modelId !== modelForm.id
}

// select-v2 的选项形状：value 用于 v-model，label 同时用于搜索与标签显示。
// 状态信息放进自定义行里的标签，label 只保留「名称 (编码)」。
// 已选但不在当前搜索结果里的设备，从缓存并回，保证标签始终有名字。
const modelDeviceSelectOptions = computed(() => {
  const list = [...modelDeviceOptions.value]
  const seen = new Set(list.map((d) => d.id))
  for (const id of modelForm.deviceIds) {
    if (seen.has(id)) continue
    const cached = deviceOptionCache.get(id)
    if (cached) {
      list.push(cached)
      seen.add(id)
    }
  }
  return list.map((d) => ({
    ...d,
    value: d.id,
    label: `${d.name} (${d.code})`,
    disabled: modelDeviceOptionDisabled(d),
  }))
})
const isModelEdit = computed(() => !!modelForm.id)

const modelRules = computed(() => ({
  code: [
    { required: true, message: t('thingModel.codeRequired'), trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_-]{0,63}$/,
      message: t('thingModel.codeFormat'),
      trigger: 'blur',
    },
  ],
  name: [
    { required: true, message: t('thingModel.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: t('thingModel.nameLength'), trigger: 'blur' },
  ],
  version: [{ max: 32, message: t('thingModel.versionLength'), trigger: 'blur' }],
  description: [{ max: 500, message: t('thingModel.descriptionLength'), trigger: 'blur' }],
}))

function resetModelForm() {
  Object.assign(modelForm, {
    id: '',
    code: '',
    name: '',
    category: '',
    version: '1.0.0',
    description: '',
    status: 1,
    deviceIds: [],
  })
}

function handleAdd() {
  resetModelForm()
  originalModelDeviceIDs.value = []
  modelDialogVisible.value = true
  nextTick(() => modelFormRef.value?.clearValidate())
  openModelDeviceOptions('')
}

function handleEdit(row) {
  Object.assign(modelForm, {
    id: row.id,
    code: row.code,
    name: row.name,
    category: row.category ?? '',
    version: row.version || '1.0.0',
    description: row.description ?? '',
    status: row.status,
    deviceIds: [],
  })
  originalModelDeviceIDs.value = []
  modelDialogVisible.value = true
  nextTick(() => modelFormRef.value?.clearValidate())
  openModelDeviceOptions(row.id)
}

function handleModelDialogClosed() {
  resetModelDeviceOptions()
  modelFormRef.value?.resetFields()
}

async function handleSubmitModel() {
  const valid = await modelFormRef.value?.validate().catch(() => false)
  if (!valid) return

  // 前端预检：把带属性的点位从当前模型里移出去，后端会以 41013 拒绝整单。
  // 先在这里说清楚，避免用户填完提交才看到错误。
  if (isModelEdit.value && modelDeviceOptionsReady.value) {
    const lockedChange = originalModelDeviceIDs.value
      .filter((id) => !modelForm.deviceIds.includes(id))
      .some((id) => modelDeviceOptions.value.find((d) => d.id === id)?.locked)
    if (lockedChange) {
      ElMessage.warning(t('thingModel.deviceUnbindLocked'))
      return
    }
  }

  modelSubmitting.value = true
  try {
    const payload = {
      name: modelForm.name,
      category: modelForm.category,
      version: modelForm.version,
      description: modelForm.description,
    }
    if (isModelEdit.value) {
      if (modelDeviceOptionsReady.value) {
        payload.deviceIds = modelForm.deviceIds
      }
      await updateThingModel({
        ...payload,
        id: modelForm.id,
        code: modelForm.code,
        status: modelForm.status,
      })
      ElMessage.success(t('thingModel.updateSuccess'))
    } else {
      await createThingModel({
        ...payload,
        code: modelForm.code,
        deviceIds: modelForm.deviceIds,
      })
      ElMessage.success(t('thingModel.createSuccess'))
    }
    modelDialogVisible.value = false
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    modelSubmitting.value = false
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('thingModel.deleteConfirm', { name: row.name }),
      t('thingModel.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return
  }
  try {
    await deleteThingModel(row.id)
    ElMessage.success(t('thingModel.deleteSuccess'))
    if (tableData.value.length === 1 && currentPage.value > 1) {
      currentPage.value -= 1
    }
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

// ---------- 属性抽屉 ----------
const propertyDrawerVisible = ref(false)
const propertyLoading = ref(false)
const properties = ref([])
const propertyPage = ref(1)
const propertyPageSize = ref(10)
const propertyTotal = ref(0)
// 模型内最大排序值：分页后前端只有当前页，默认排序必须由服务端给出
const propertyMaxSortOrder = ref(0)
const currentModel = ref(null)

const propertyDrawerTitle = computed(() =>
  currentModel.value
    ? t('thingModel.propertiesTitle', { name: currentModel.value.name })
    : t('thingModel.propertiesTitle', { name: '' }),
)

async function openProperties(row) {
  currentModel.value = row
  propertyPage.value = 1
  propertyDrawerVisible.value = true
  await fetchProperties()
}

async function fetchProperties() {
  if (!currentModel.value) return
  propertyLoading.value = true
  try {
    const res = await pageThingModelProperties(currentModel.value.id, {
      page: propertyPage.value,
      size: propertyPageSize.value,
    })
    properties.value = res?.records ?? []
    propertyTotal.value = res?.total ?? 0
    propertyMaxSortOrder.value = res?.maxSortOrder ?? 0
  } catch {
    properties.value = []
    propertyTotal.value = 0
    propertyMaxSortOrder.value = 0
  } finally {
    propertyLoading.value = false
  }
}

function handlePropertySizeChange(size) {
  propertyPageSize.value = size
  propertyPage.value = 1
  fetchProperties()
}

function handlePropertyCurrentChange(page) {
  propertyPage.value = page
  fetchProperties()
}

function handleDrawerClosed() {
  currentModel.value = null
  properties.value = []
  propertyPage.value = 1
  propertyTotal.value = 0
  propertyMaxSortOrder.value = 0
}

// ---------- 属性绑定点位抽屉 ----------
const propertyPointsDrawerVisible = ref(false)
const propertyPointsLoading = ref(false)
const propertyPoints = ref([])
const propertyPointsProperty = ref(null)
let propertyPointsToken = 0

const propertyPointsTitle = computed(() =>
  t('thingModel.propertyPointsTitle', {
    identifier: propertyPointsProperty.value?.identifier ?? '',
    name: propertyPointsProperty.value?.name ?? '',
  }),
)

async function openPropertyPoints(row) {
  propertyPointsProperty.value = row
  propertyPoints.value = []
  propertyPointsDrawerVisible.value = true
  await fetchPropertyPoints()
}

async function fetchPropertyPoints() {
  if (!propertyPointsProperty.value) return
  const token = ++propertyPointsToken
  propertyPointsLoading.value = true
  try {
    const rows = (await listThingModelPropertyPoints(propertyPointsProperty.value.id)) || []
    if (token !== propertyPointsToken) return
    propertyPoints.value = rows
  } catch {
    if (token === propertyPointsToken) propertyPoints.value = []
  } finally {
    if (token === propertyPointsToken) propertyPointsLoading.value = false
  }
}

function handlePropertyPointsClosed() {
  propertyPointsToken += 1
  propertyPointsProperty.value = null
  propertyPoints.value = []
}

// ---------- 已绑定设备抽屉 ----------
const deviceDrawerVisible = ref(false)
const boundDevicesLoading = ref(false)
const boundDevices = ref([])
const deviceDrawerModel = ref(null)
let deviceDrawerToken = 0

const deviceDrawerTitle = computed(() =>
  t('thingModel.devicesTitle', { name: deviceDrawerModel.value?.name ?? '' }),
)

async function openDevices(row) {
  deviceDrawerModel.value = row
  boundDevices.value = []
  deviceDrawerVisible.value = true
  await fetchBoundDevices()
}

async function fetchBoundDevices() {
  if (!deviceDrawerModel.value) return
  const token = ++deviceDrawerToken
  boundDevicesLoading.value = true
  try {
    const rows = (await listThingModelDevices(deviceDrawerModel.value.id)) || []
    if (token !== deviceDrawerToken) return
    boundDevices.value = rows
  } catch {
    if (token === deviceDrawerToken) boundDevices.value = []
  } finally {
    if (token === deviceDrawerToken) boundDevicesLoading.value = false
  }
}

function handleDeviceDrawerClosed() {
  deviceDrawerToken += 1
  deviceDrawerModel.value = null
  boundDevices.value = []
}

// ---------- 属性表单 ----------
const propertyDialogVisible = ref(false)
const propertySubmitting = ref(false)
const propertyFormRef = ref(null)
const propertyForm = reactive({
  id: '',
  identifier: '',
  name: '',
  kind: 'float',
  accessMode: 'R',
  unit: '',
  description: '',
  sortOrder: 10,
  status: 1,
  addressIds: [],
})
const isPropertyEdit = computed(() => !!propertyForm.id)

// ---------- 点位绑定候选 ----------
// 只取已绑定当前模型的设备下的点位；选项加载失败时不发送 addressIds，
// 避免把一次失败的请求变成「全部解绑」。
const propertyPointOptions = ref([])
const propertyPointOptionsLoading = ref(false)
const propertyPointOptionsReady = ref(false)
const originalPropertyAddressIDs = ref([])
let pointOptionsToken = 0

async function loadPropertyPointOptions(modelId, propertyId) {
  if (!modelId) return
  const token = ++pointOptionsToken
  propertyPointOptionsLoading.value = true
  propertyPointOptionsReady.value = false
  try {
    const rows = (await listThingModelPointOptions(modelId)) || []
    if (token !== pointOptionsToken) return
    propertyPointOptions.value = rows
    propertyPointOptionsReady.value = true
    const bound = propertyId
      ? rows.filter((p) => p.propertyId === propertyId).map((p) => p.addressId)
      : []
    originalPropertyAddressIDs.value = bound
    propertyForm.addressIds = [...bound]
  } catch {
    if (token === pointOptionsToken) {
      propertyPointOptions.value = []
      propertyPointOptionsReady.value = false
    }
  } finally {
    if (token === pointOptionsToken) propertyPointOptionsLoading.value = false
  }
}

// 按设备分组展示，避免多台设备的同名地址混在一起分不清。
const propertyPointGroups = computed(() => {
  const groups = []
  const byDevice = new Map()
  for (const p of propertyPointOptions.value) {
    let g = byDevice.get(p.deviceId)
    if (!g) {
      g = {
        deviceId: p.deviceId,
        deviceLabel: p.deviceName || p.deviceCode || p.deviceId,
        points: [],
      }
      byDevice.set(p.deviceId, g)
      groups.push(g)
    }
    g.points.push(p)
  }
  return groups
})

// 与后端 accessCovers 同一规则（R 只覆盖 R，W 只覆盖 W，RW 全覆盖）。
function accessCovers(addressRW, propertyAccess) {
  switch (addressRW) {
    case 'RW':
      return true
    case 'R':
      return propertyAccess === 'R'
    case 'W':
      return propertyAccess === 'W'
    default:
      return false
  }
}

function pointOptionDisabled(p) {
  // 当前属性已绑定的点位始终可选：改 kind/access 后用户仍要能取消勾选。
  if (p.propertyId === propertyForm.id && p.propertyId) return false
  if (p.propertyId) return true
  if (!p.kind) return true
  if (p.kind !== propertyForm.kind) return true
  return !accessCovers(p.rwPermission, propertyForm.accessMode)
}

function pointOptionLabel(p) {
  const parts = [`${p.label || p.name} (${p.name})`]
  if (p.propertyId && p.propertyId !== propertyForm.id) {
    parts.push(
      t('thingModel.pointBoundElsewhereShort', {
        name: p.propertyName || p.propertyIdentifier || p.propertyId,
      }),
    )
    return `${parts[0]} · ${parts[1]}`
  }
  if (!p.kind) {
    parts.push(t('thingModel.pointKindUnknown'))
  } else if (p.kind !== propertyForm.kind) {
    parts.push(t('thingModel.pointKindMismatch'))
  } else if (!accessCovers(p.rwPermission, propertyForm.accessMode)) {
    parts.push(t('thingModel.pointAccessMismatch'))
  }
  return parts.length > 1 ? `${parts[0]} · ${parts.slice(1).join(' / ')}` : parts[0]
}

// 同一设备对同一属性只能绑一个点位（后端返回 41021，迁移里有部分唯一索引）。
// 多选在设备维度上退化为单选：以“最后选中的”为准，静默替换同设备已选点，
// 避免用户选完提交才看到错误。
watch(
  () => propertyForm.addressIds,
  (next, prev) => {
    if (!Array.isArray(next) || next.length < 2) return
    const prevSet = new Set(prev || [])
    const added = next.filter((id) => !prevSet.has(id))
    if (added.length === 0) return

    const winners = new Map()
    for (const id of added) {
      const p = propertyPointOptions.value.find((x) => x.addressId === id)
      if (p) winners.set(p.deviceId, id)
    }
    if (winners.size === 0) return
    const current = next.filter((id) => {
      const p = propertyPointOptions.value.find((x) => x.addressId === id)
      if (!p || !winners.has(p.deviceId)) return true
      return winners.get(p.deviceId) === id
    })
    if (current.length !== next.length) {
      propertyForm.addressIds = current
    }
  },
)

// kind / accessMode 变化后，已选但不再兼容的“新增”点位自动移除；
// 原本就绑在本属性上的点位保留，交由后端 41018 阻止改类型。
watch([() => propertyForm.kind, () => propertyForm.accessMode], () => {
  if (!propertyDialogVisible.value || propertyForm.addressIds.length === 0) return
  const originals = new Set(originalPropertyAddressIDs.value)
  const next = propertyForm.addressIds.filter((id) => {
    if (originals.has(id)) return true
    const p = propertyPointOptions.value.find((x) => x.addressId === id)
    return p ? !pointOptionDisabled(p) : false
  })
  if (next.length !== propertyForm.addressIds.length) {
    propertyForm.addressIds = next
    ElMessage.warning(t('thingModel.bindPointsPruned'))
  }
})

const propertyRules = computed(() => ({
  identifier: [
    { required: true, message: t('thingModel.identifierRequired'), trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_]{0,63}$/,
      message: t('thingModel.identifierFormat'),
      trigger: 'blur',
    },
  ],
  name: [
    { required: true, message: t('thingModel.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: t('thingModel.nameLength'), trigger: 'blur' },
  ],
  kind: [{ required: true, message: t('thingModel.kindRequired'), trigger: 'change' }],
  accessMode: [{ required: true, message: t('thingModel.accessRequired'), trigger: 'change' }],
  unit: [{ max: 32, message: t('thingModel.unitLength'), trigger: 'blur' }],
  description: [{ max: 500, message: t('thingModel.descriptionLength'), trigger: 'blur' }],
}))

function nextSortOrder() {
  return (Number(propertyMaxSortOrder.value) || 0) + 10
}

function handleAddProperty() {
  Object.assign(propertyForm, {
    id: '',
    identifier: '',
    name: '',
    kind: 'float',
    accessMode: 'R',
    unit: '',
    description: '',
    sortOrder: nextSortOrder(),
    status: 1,
    addressIds: [],
  })
  originalPropertyAddressIDs.value = []
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
  loadPropertyPointOptions(currentModel.value?.id, '')
}

function handleEditProperty(row) {
  Object.assign(propertyForm, {
    id: row.id,
    identifier: row.identifier,
    name: row.name,
    kind: row.kind,
    accessMode: row.accessMode,
    unit: row.unit ?? '',
    description: row.description ?? '',
    sortOrder: row.sortOrder ?? 0,
    status: row.status,
    addressIds: [],
  })
  originalPropertyAddressIDs.value = []
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
  loadPropertyPointOptions(currentModel.value?.id, row.id)
}

function handlePropertyDialogClosed() {
  pointOptionsToken += 1
  propertyPointOptions.value = []
  propertyPointOptionsLoading.value = false
  propertyPointOptionsReady.value = false
  originalPropertyAddressIDs.value = []
  propertyFormRef.value?.resetFields()
}

async function handleSubmitProperty() {
  if (!currentModel.value) return
  const valid = await propertyFormRef.value?.validate().catch(() => false)
  if (!valid) return

  // 编辑态要在关闭弹窗前取：关闭后 resetFields 会清掉 propertyForm.id
  const isEdit = isPropertyEdit.value
  propertySubmitting.value = true
  try {
    const payload = {
      name: propertyForm.name,
      kind: propertyForm.kind,
      accessMode: propertyForm.accessMode,
      unit: propertyForm.unit,
      description: propertyForm.description,
      sortOrder: propertyForm.sortOrder,
    }
    if (isEdit) {
      // 选项没加载成功就不碰绑定：发送 [] 会把原有绑定全部清掉。
      if (propertyPointOptionsReady.value) {
        payload.addressIds = propertyForm.addressIds
      }
      await updateThingModelProperty({
        ...payload,
        id: propertyForm.id,
        identifier: propertyForm.identifier,
        status: propertyForm.status,
      })
      ElMessage.success(t('thingModel.propertyUpdateSuccess'))
    } else {
      await createThingModelProperty({
        ...payload,
        modelId: currentModel.value.id,
        identifier: propertyForm.identifier,
        addressIds: propertyForm.addressIds,
      })
      ElMessage.success(t('thingModel.propertyCreateSuccess'))
    }
    propertyDialogVisible.value = false
    // 新增属性排在末尾（sortOrder = 全量最大值 + 10），翻到末页才看得见
    if (!isEdit) {
      propertyPage.value = Math.max(
        1,
        Math.ceil((propertyTotal.value + 1) / propertyPageSize.value),
      )
    }
    await fetchProperties()
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    propertySubmitting.value = false
  }
}

async function handleDeleteProperty(row) {
  if (!currentModel.value) return
  try {
    await ElMessageBox.confirm(
      t('thingModel.deletePropertyConfirm', { name: row.name }),
      t('thingModel.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return
  }
  try {
    await deleteThingModelProperty(row.id)
    ElMessage.success(t('thingModel.propertyDeleteSuccess'))
    if (properties.value.length === 1 && propertyPage.value > 1) {
      propertyPage.value -= 1
    }
    await fetchProperties()
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

// ---------- Excel 导入 / 模板下载 ----------
const {
  fileInputRef: modelFileInputRef,
  importing: modelImporting,
  importResultVisible: modelImportResultVisible,
  importResult: modelImportResult,
  handleImport: handleModelImport,
  handleFileChange: handleModelFileChange,
  handleDownloadTemplate: handleModelTemplateDownload,
} = useExcelImport({
  importFn: (file) => importThingModels(file),
  downloadFn: downloadThingModelTemplate,
  templateName: () => t('thingModel.importModelTemplateName'),
  refresh: fetchList,
})

const {
  fileInputRef: propertyFileInputRef,
  importing: propertyImporting,
  importResultVisible: propertyImportResultVisible,
  importResult: propertyImportResult,
  handleImport: handlePropertyImport,
  handleFileChange: handlePropertyFileChange,
  handleDownloadTemplate: handlePropertyTemplateDownload,
} = useExcelImport({
  importFn: (file) => importThingModelProperties(file, currentModel.value?.id),
  downloadFn: downloadThingModelPropertyTemplate,
  templateName: () => t('thingModel.importPropertyTemplateName'),
  refresh: async () => {
    await fetchProperties()
    fetchList()
  },
})

onMounted(fetchList)
</script>

<style scoped>
.thing-category-select {
  width: 150px;
}

.thing-name {
  color: var(--ink-2);
  font-weight: 500;
}

.thing-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.thing-drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 16px;
}

.thing-drawer-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.thing-drawer-actions :deep(.hidden) {
  display: none;
}

.thing-drawer-sub {
  margin: 0;
  color: var(--ink-3);
  font-size: 13px;
  line-height: 1.6;
}

.thing-hint-warning {
  color: var(--warning);
}

/* 设备候选行：名称+编码靠左成组，状态标签靠右，中间用弹性留白隔开 */
.thing-device-option {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-width: 0;
}

.thing-device-option-main {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.thing-device-option-name {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink);
}

/* 编码做成紧贴名称的等宽小标签，与右侧「模型 / 锁定」状态标签明确分开 */
.thing-device-option-code {
  flex: 0 0 auto;
  max-width: 45%;
  padding: 0 6px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 11px;
  line-height: 20px;
  color: var(--ink-3);
  background: var(--line-soft);
  border-radius: 4px;
}

.thing-device-option-tags {
  display: flex;
  flex: none;
  align-items: center;
  gap: 6px;
  margin-left: auto;
}

.thing-device-option-tag {
  max-width: 150px;
  overflow: hidden;
}

.thing-device-option-tag :deep(.el-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.thing-device-option-footer {
  padding: 6px 12px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-4);
  border-top: 1px solid var(--line-soft);
}

/* 属性点位抽屉里的设备单元格：名称在上、编码在下，避免与地址列混淆 */
.thing-point-device {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.thing-point-device-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink);
}

.thing-point-device-code {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--ink-4);
}
</style>
