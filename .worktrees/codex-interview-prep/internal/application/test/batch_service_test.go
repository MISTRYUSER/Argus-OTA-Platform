package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

// MockBatchRepository - BatchRepository 的 Mock 实现
type MockBatchRepository struct {
	mock.Mock
}

func (m *MockBatchRepository) Save(ctx context.Context, batch *domain.Batch) error {
	args := m.Called(ctx, batch)
	return args.Error(0)
}

func (m *MockBatchRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Batch, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) FindByVIN(ctx context.Context, vin string) ([]*domain.Batch, error) {
	args := m.Called(ctx, vin)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) FindByStatus(ctx context.Context, status domain.BatchStatus) ([]*domain.Batch, error) {
	args := m.Called(ctx, status)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) List(ctx context.Context, opts domain.ListOptions) ([]*domain.Batch, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockBatchRepository) FindStuckBatches(ctx context.Context) ([]*domain.Batch, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

// MockFileRepository - P0 fix: FileRepository Mock
type MockFileRepository struct {
	mock.Mock
}

func (m *MockFileRepository) Save(ctx context.Context, file *domain.File) error {
	args := m.Called(ctx, file)
	return args.Error(0)
}

func (m *MockFileRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.File, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.File), args.Error(1)
}

func (m *MockFileRepository) FindByBatchID(ctx context.Context, batchID uuid.UUID) ([]*domain.File, error) {
	args := m.Called(ctx, batchID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.File), args.Error(1)
}

func (m *MockFileRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockFileRepository) UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status domain.ProcessingStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

// MockKafkaEventPublisher - KafkaEventPublisher 的 Mock 实现
type MockKafkaEventPublisher struct {
	mock.Mock
}

func (m *MockKafkaEventPublisher) PublishEvents(ctx context.Context, events []domain.DomainEvent) error {
	args := m.Called(ctx, events)
	return args.Error(0)
}

func (m *MockKafkaEventPublisher) Close() error {
	args := m.Called()
	return args.Error(0)
}

// TestCreateBatch_Success - 测试成功创建 Batch
func TestCreateBatch_Success(t *testing.T) {
	// 1. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 2. 设置期望（P2 修复：两阶段上传设计，只保存一次，不发布事件）
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil).Times(1)
	// 注意：CreateBatch 不再发布 Kafka 事件，事件在 CompleteUpload 时发布

	// 3. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	// 4. 执行测试
	ctx := context.Background()
	batch, err := service.CreateBatch(ctx, "vehicle-001", "VIN123", 5)

	// 5. 验证结果
	assert.NoError(t, err)
	assert.NotNil(t, batch)
	assert.Equal(t, "vehicle-001", batch.VehicleID)
	assert.Equal(t, "VIN123", batch.VIN)
	assert.Equal(t, 5, batch.ExpectedWorkerCount)
	assert.Equal(t, domain.BatchStatusPending, batch.Status)

	// 6. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
	mockKafka.AssertExpectations(t)
}

// TestCreateBatch_RepositoryError - 测试 Repository 保存失败
func TestCreateBatch_RepositoryError(t *testing.T) {
	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(errors.New("database error"))

	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	ctx := context.Background()
	batch, err := service.CreateBatch(ctx, "vehicle-001", "VIN123", 5)

	assert.Error(t, err)
	assert.Nil(t, batch)
	assert.Contains(t, err.Error(), "database error")

	mockRepo.AssertExpectations(t)
}

// TestTransitionBatchStatus_Success - 测试成功转换状态
func TestTransitionBatchStatus_Success(t *testing.T) {
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)
	testBatch.TransitionTo(domain.BatchStatusUploaded)

	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil).Once()
	mockKafka.On("PublishEvents", mock.Anything, mock.AnythingOfType("[]domain.DomainEvent")).Return(nil)

	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	ctx := context.Background()
	err := service.TransitionBatchStatus(ctx, testBatch.ID, domain.BatchStatusScattering)

	assert.NoError(t, err)
	mockRepo.AssertExpectations(t)
	mockKafka.AssertExpectations(t)
}

func TestTransitionBatchStatus_PublishFailedShouldReturnError(t *testing.T) {
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)
	testBatch.TransitionTo(domain.BatchStatusUploaded)

	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil).Once()
	mockKafka.On("PublishEvents", mock.Anything, mock.AnythingOfType("[]domain.DomainEvent")).Return(errors.New("kafka unavailable")).Once()

	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	err := service.TransitionBatchStatus(context.Background(), testBatch.ID, domain.BatchStatusScattering)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to publish events")

	mockRepo.AssertExpectations(t)
	mockKafka.AssertExpectations(t)
}

// TestTransitionBatchStatus_BatchNotFound - 测试 Batch 不存在
func TestTransitionBatchStatus_BatchNotFound(t *testing.T) {
	batchID := uuid.New()

	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	mockRepo.On("FindByID", mock.Anything, batchID).Return(nil, nil)

	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	ctx := context.Background()
	err := service.TransitionBatchStatus(ctx, batchID, domain.BatchStatusScattering)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "batch not found")

	mockRepo.AssertExpectations(t)
}

// TestAddFile_Success - 测试成功添加文件
func TestAddFile_Success(t *testing.T) {
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)
	fileID := uuid.New()

	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil)
	mockFileRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.File")).Return(nil)

	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	ctx := context.Background()
	err := service.AddFile(ctx, testBatch.ID, fileID, "test.dat", 1024, "minio/test.dat")

	assert.NoError(t, err)
	assert.Equal(t, 1, testBatch.TotalFiles)

	mockRepo.AssertExpectations(t)
	mockFileRepo.AssertExpectations(t)
}

// TestAddFile_WrongStatus - 测试在错误状态下添加文件
func TestAddFile_WrongStatus(t *testing.T) {
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)

	testBatch.TransitionTo(domain.BatchStatusUploaded)
	testBatch.TransitionTo(domain.BatchStatusScattering)

	assert.Equal(t, domain.BatchStatusScattering, testBatch.Status)

	fileID := uuid.New()

	mockRepo := new(MockBatchRepository)
	mockFileRepo := new(MockFileRepository)
	mockKafka := new(MockKafkaEventPublisher)

	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)

	service := application.NewBatchService(mockRepo, mockFileRepo, mockKafka)

	ctx := context.Background()
	err := service.AddFile(ctx, testBatch.ID, fileID, "test.dat", 1024, "minio/test.dat")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in pending or uploaded status")

	mockRepo.AssertExpectations(t)
}
