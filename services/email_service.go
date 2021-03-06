package services

import (
	"fmt"
	"github.com/gocms-io/gocms/context"
	"github.com/gocms-io/gocms/models"
	"github.com/gocms-io/gocms/repositories"
	"github.com/gocms-io/gocms/utility/errors"
	"log"
	"time"
)

type IEmailService interface {
	SetVerified(email string) error
	GetVerified(email string) bool
	AddEmail(email *models.Email) error
	GetEmailsByUserId(userId int) ([]models.Email, error)
	SendEmailActivationCode(email string) error
	VerifyEmailActivationCode(id int, code string) bool
	PromoteEmail(email *models.Email) error
	DeleteEmail(email *models.Email) error
}

type EmailService struct {
	MailService       IMailService
	AuthService       IAuthService
	RepositoriesGroup *repositories.RepositoriesGroup
}

func DefaultEmailService(rg *repositories.RepositoriesGroup, ms *MailService, as *AuthService) *EmailService {
	emailService := &EmailService{
		RepositoriesGroup: rg,
		AuthService:       as,
		MailService:       ms,
	}
	return emailService
}

func (es *EmailService) SetVerified(e string) error {
	// get email
	email, err := es.RepositoriesGroup.EmailRepository.GetByAddress(e)
	if err != nil {
		return err
	}

	// set verified
	email.IsVerified = true
	err = es.RepositoriesGroup.EmailRepository.Update(email)
	if err != nil {
		return err
	}
	return err
}

func (es *EmailService) GetVerified(e string) bool {
	email, err := es.RepositoriesGroup.EmailRepository.GetByAddress(e)
	if err != nil {
		log.Printf("Email Service, Get Verified, Error getting email by address: %s\n", err.Error())
		return false
	}

	return email.IsVerified
}

func (es *EmailService) AddEmail(e *models.Email) error {

	// check to see if email exist
	emailExists, _ := es.RepositoriesGroup.EmailRepository.GetByAddress(e.Email)
	if emailExists != nil {
		return errors.NewToUser("Email already exists.")
	}

	// add email
	err := es.RepositoriesGroup.EmailRepository.Add(e)
	if err != nil {
		log.Printf("Email Service, error adding email: %s", err.Error())
		return err
	}

	// send email to primary email about addition of email
	if primaryEmail, err := es.RepositoriesGroup.EmailRepository.GetPrimaryByUserId(e.UserId); err == nil {
		mail := Mail{
			To:      primaryEmail.Email,
			Subject: "New Email Added To Your Account",
			Body:    "A new alternative email address, " + e.Email + ", was added to your account.\n\n If you believe this to be a mistake please contact support.",
		}
		es.MailService.Send(&mail)
	}

	return nil
}

func (es *EmailService) SendEmailActivationCode(emailAddress string) error {

	// get userId from email
	email, err := es.RepositoriesGroup.EmailRepository.GetByAddress(emailAddress)
	if err != nil {
		fmt.Printf("Error sending email activation code, get email: %s", err.Error())
		return err
	}

	if email.IsVerified {
		err = errors.NewToUser("Email already activated.")
		fmt.Printf("Error sending email activation code, %s\n", err.Error())
		return err
	}

	// create reset code
	code, hashedCode, err := es.AuthService.GetRandomCode(32)
	if err != nil {
		fmt.Printf("Error sending email activation code, get random code: %s\n", err.Error())
		return err
	}

	// update user with new code
	err = es.RepositoriesGroup.SecureCodeRepository.Add(&models.SecureCode{
		UserId: email.UserId,
		Type:   models.Code_VerifyEmail,
		Code:   hashedCode,
	})
	if err != nil {
		fmt.Printf("Error sending email activation code, add secure code: %s\n", err.Error())
		return err
	}

	// send email
	es.MailService.Send(&Mail{
		To:      emailAddress,
		Subject: "Email Verification Required",
		Body: "Click on the link below to activate your email:\n" +
			context.Config.PublicApiUrl + "/user/email/activate?code=" + code + "&email=" + emailAddress + "\n\nThe link will expire at: " +
			time.Now().Add(time.Minute*time.Duration(context.Config.PasswordResetTimeout)).String() + ".",
	})
	if err != nil {
		log.Println("Error sending email activation code, sending mail: " + err.Error())
	}

	return nil
}

func (es *EmailService) VerifyEmailActivationCode(id int, code string) bool {

	// get code
	secureCode, err := es.RepositoriesGroup.SecureCodeRepository.GetLatestForUserByType(id, models.Code_VerifyEmail)
	if err != nil {
		log.Printf("error getting latest password reset code: %s", err.Error())
		return false
	}

	if ok := es.AuthService.VerifyPassword(secureCode.Code, code); !ok {
		return false
	}

	// check within time
	if time.Since(secureCode.Created) > (time.Minute * time.Duration(context.Config.PasswordResetTimeout)) {
		return false
	}

	err = es.RepositoriesGroup.SecureCodeRepository.Delete(secureCode.Id)
	if err != nil {
		return false
	}

	return true
}

func (es *EmailService) PromoteEmail(email *models.Email) error {

	// get email for verification
	dbEmail, err := es.RepositoriesGroup.EmailRepository.GetByAddress(email.Email)
	if err != nil {
		log.Printf("email service, promote email, get by address, error: %s", err.Error())
		return err
	}

	// verify email owner
	if email.UserId != dbEmail.UserId {
		err = errors.NewToUser("You can only promote email address owned by you.")
		log.Printf("email service, promote email, get by address, error: %s", err.Error())
		return err
	}

	// email must be verified
	if !dbEmail.IsVerified {
		err = errors.NewToUser("You can only promote an email address after it has been validated.")
		log.Printf("email service, promote email, get by address, error: %s", err.Error())
		return err
	}

	// get user primary email to send notification too first
	oldPrimaryEmail, err := es.RepositoriesGroup.EmailRepository.GetPrimaryByUserId(email.UserId)
	if err != nil {
		log.Printf("email service, promote email, get primary by userId, error: %s", err.Error())
		return err
	}

	// promote email
	err = es.RepositoriesGroup.EmailRepository.PromoteEmail(dbEmail.Id, dbEmail.UserId)
	if err != nil {
		log.Printf("email service, promote email, promoting email, errors:%s", err.Error())
		return err
	}

	// send notification
	// send email to primary email about addition of email
	mail := Mail{
		To:      oldPrimaryEmail.Email,
		Subject: "A New Primary Email Has Been Set",
		Body:    "A new primary email address, " + email.Email + ", has been set on your account.\n\n If you believe this to be a mistake please contact support.",
	}
	es.MailService.Send(&mail)

	return nil
}

func (es *EmailService) GetEmailsByUserId(userId int) ([]models.Email, error) {

	// get all emails
	emails, err := es.RepositoriesGroup.EmailRepository.GetByUserId(userId)
	if err != nil {
		log.Printf("email service, get emails by user id, error: %s", err.Error())
		return nil, err
	}

	return emails, nil
}

func (es *EmailService) DeleteEmail(email *models.Email) error {

	// get email for verification
	dbEmail, err := es.RepositoriesGroup.EmailRepository.GetByAddress(email.Email)
	if err != nil {
		log.Printf("email service, promote email, get by address, error: %s", err.Error())
		return err
	}

	// verify email owner
	if email.UserId != dbEmail.UserId {
		err = errors.NewToUser("You can only delete email address owned by you.")
		log.Printf("email service, delete email, get by address, error: %s", err.Error())
		return err
	}

	// email cannot be primary
	if dbEmail.IsPrimary {
		err = errors.NewToUser("You can't delete the primary email address from an account.")
		log.Printf("email service, delete email, get by address, error: %s", err.Error())
		return err
	}

	// delete email
	err = es.RepositoriesGroup.EmailRepository.Delete(dbEmail.Id)
	if err != nil {
		log.Printf("email service, delete email, deleting email, errors:%s", err.Error())
		return err
	}

	// get user primary email to send notification too
	primaryEmail, err := es.RepositoriesGroup.EmailRepository.GetPrimaryByUserId(email.UserId)
	if err != nil {
		log.Printf("email service, delete email, get primary by userId, error: %s", err.Error())
		return err
	}

	// send notification
	// send email to primary email about addition of email
	mail := Mail{
		To:      primaryEmail.Email,
		Subject: "Alternative Email Delete From Account",
		Body:    "An alternative email, " + email.Email + ", has been deleted from your account.\n\n If you believe this to be a mistake please contact support.",
	}
	es.MailService.Send(&mail)

	return nil
}
